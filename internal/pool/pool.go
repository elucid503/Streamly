package pool

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"streamly/internal/captions"
	"streamly/internal/config"
	"streamly/internal/selfbot"
	"streamly/internal/source"
	"streamly/internal/streamer"
	"streamly/internal/transcode"
	"streamly/internal/workers"
)

var (
	ErrNoWorker = errors.New("No worker is configured for your server.")
	ErrWorkerBusy = errors.New("A stream is already active in this server.")
	ErrKeyChangeActive = errors.New("cannot change key while a stream is active")
)

type CloseReason string

const (
	CloseEnded CloseReason = "ended"
	CloseStopped CloseReason = "stopped"
	CloseError CloseReason = "error"
	CloseSwapped CloseReason = "swapped"
)

const hotswapTeardownTimeout = 20 * time.Second

type QualityResolver func(attempt int) (string, error)

type Request struct {

	GuildID string
	ChannelID string
	Caption string

	InitialURL string
	ResolveURL source.UrlResolver
	QualityURL QualityResolver
	QualityLabel string
	Headers map[string]string
	ResolveHeaders func() map[string]string

	// ResolveFallbackURL and ResolveFallbackHeaders are used after liveProviderFallbackAfter
	// consecutive live reconnect failures to try an alternate stream source.
	ResolveFallbackURL source.UrlResolver
	ResolveFallbackHeaders func() map[string]string

	Live bool

	Metadata *StreamMetadata

	OnPrepare func(*Session)
	OnMediaProbed func(*Session, int64)
	OnNearEnd func()
	OnClose func(CloseReason)

}

type Session struct {

	ID string
	Streamer *streamer.Streamer
	Client *selfbot.Client

	Busy bool
	Paused bool // Pausing stalls libav reads and frame pacing.
	StopRequested bool

	controller context.CancelFunc
	media *source.MediaSource
	request *Request

	startedAt time.Time
	pausedAt time.Time
	stats *source.MediaSourceStats

	transcodePause func()
	transcodeResume func()

	seekMu sync.Mutex
	pendingSeek *time.Duration
	segmentCancel context.CancelFunc

	Captions *captions.Track
	FontsDir string
	CaptionSource string
	CaptionQueryKey string

	Metadata *StreamMetadata
	ctaFontPath string
	pendingSegmentCTAs []SegmentCTA
	timedCTAs []TimedCTA
	creditsTriggerMs int64
	nearEndTriggered bool

	liveAttempt int

	swapping bool
	loopDone chan struct{}

}

func (session *Session) setSegmentCancel(cancel context.CancelFunc) {

	session.seekMu.Lock()
	defer session.seekMu.Unlock()

	session.segmentCancel = cancel

}

func (session *Session) takeSeek() (time.Duration, bool) {

	session.seekMu.Lock()
	defer session.seekMu.Unlock()

	if session.pendingSeek == nil {

		return 0, false

	}

	target := *session.pendingSeek
	session.pendingSeek = nil

	return target, true

}

func (session *Session) seekPending() bool {

	session.seekMu.Lock()
	defer session.seekMu.Unlock()

	return session.pendingSeek != nil

}

type Stats struct {

	ID string
	Caption string
	ChannelID string

	Paused bool
	CaptionsEnabled bool
	CaptionSource string

	UptimeMs int64
	BytesRead int64
	QualityLabel string
	PositionMs int64
	DurationMs *int64

}

type Pool struct {

	mu sync.Mutex
	sessions map[string]*Session
	store *workers.Store

}

func New(store *workers.Store) *Pool {

	return &Pool{sessions: make(map[string]*Session), store: store}

}

func (p *Pool) Size() int {

	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.sessions)

}

func (p *Pool) LoadWorkers(ctx context.Context) error {

	if err := p.store.Load(); err != nil {

		return err

	}

	entries := p.store.All()

	var wg sync.WaitGroup

	for guildID, entry := range entries {

		wg.Add(1)

		go func(guildID, token string) {

			defer wg.Done()

			if err := p.addWorker(ctx, guildID, token); err != nil {

				log.Printf("worker for guild %s failed to log in: %v", guildID, err)

			}

		}(guildID, entry.Token)

	}

	wg.Wait()

	return nil

}

func (p *Pool) RequireAvailable(guildID string) error {

	p.mu.Lock()
	defer p.mu.Unlock()

	session, ok := p.sessions[guildID]

	if !ok {

		return ErrNoWorker

	}

	if session.Busy {

		return ErrWorkerBusy

	}

	return nil

}

// RequireWorker verifies a worker exists for the guild without rejecting an
// already-active stream; the active stream can be hot-swapped in place.
func (p *Pool) RequireWorker(guildID string) error {

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.sessions[guildID]; !ok {

		return ErrNoWorker

	}

	return nil

}

func (p *Pool) Acquire(guildID string) (*Session, error) {

	p.mu.Lock()
	defer p.mu.Unlock()

	session, ok := p.sessions[guildID]

	if !ok {

		return nil, ErrNoWorker

	}

	if session.Busy {

		return nil, ErrWorkerBusy

	}

	session.Busy = true
	session.Paused = false
	session.StopRequested = false
	session.stats = &source.MediaSourceStats{}

	session.Captions = &captions.Track{}
	session.FontsDir = ""
	session.CaptionSource = ""
	session.CaptionQueryKey = ""

	session.Metadata = nil
	session.ctaFontPath = ""
	session.pendingSegmentCTAs = nil
	session.timedCTAs = nil
	session.creditsTriggerMs = 0
	session.nearEndTriggered = false
	session.liveAttempt = 0

	return session, nil

}

func (p *Pool) Get(id string) *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.sessions[id]

}

func (session *Session) Live() bool {

	if session.request == nil {

		return false

	}

	return session.request.Live

}

func (p *Pool) ActiveInGuild(guildID string) *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	session := p.sessions[guildID]

	if session != nil && session.Busy {

		return session

	}

	return nil

}

func (p *Pool) Active(guildID, channelID string) *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	session := p.sessions[guildID]

	if session == nil || !session.Busy || session.request == nil {

		return nil

	}

	if channelID != "" && session.request.ChannelID != channelID {

		return nil

	}

	return session

}

func (p *Pool) Stats(session *Session) Stats {

	now := time.Now()

	if session.Paused && !session.pausedAt.IsZero() {

		now = session.pausedAt

	}

	uptime := int64(0)

	if !session.startedAt.IsZero() {

		uptime = now.Sub(session.startedAt).Milliseconds()

	}

	position := p.positionMs(session)

	return Stats{

		ID: session.ID,
		Caption: captionOf(session),
		ChannelID: channelOf(session),

		Paused: session.Paused,
		CaptionsEnabled: session.Captions != nil && session.Captions.Enabled(),
		CaptionSource: session.CaptionSource,

		UptimeMs: uptime,
		BytesRead: session.stats.BytesRead,
		QualityLabel: qualityOf(session),
		PositionMs: position,
		DurationMs: session.stats.DurationMs,

	}

}

func (p *Pool) positionMs(session *Session) int64 {

	if session.request == nil || session.startedAt.IsZero() {

		return 0

	}

	now := time.Now()

	if session.Paused && !session.pausedAt.IsZero() {

		now = session.pausedAt

	}

	elapsed := now.Sub(session.startedAt).Milliseconds()

	if session.stats.DurationMs != nil {

		return min64(elapsed, *session.stats.DurationMs)

	}

	return elapsed

}

func (p *Pool) Play(ctx context.Context, session *Session, request Request) error {

	session.StopRequested = false
	session.request = &request
	session.startedAt = time.Now()
	session.pausedAt = time.Time{}
	session.stats = &source.MediaSourceStats{}

	log.Printf(`[stream] joining voice channel %s for "%s"`, request.ChannelID, request.Caption)

	if _, err := session.Streamer.JoinVoice(ctx, request.GuildID, request.ChannelID); err != nil {

		log.Printf(`[stream] voice join failed for "%s": %v`, request.Caption, err)
		return err

	}

	log.Printf(`[stream] voice joined; starting pipeline for "%s"`, request.Caption)

	session.Streamer.SetOnVoiceLeave(func() {

		if !session.Busy {

			return

		}

		if session.controller != nil {

			session.controller()

		}

	})

	session.Metadata = request.Metadata

	if request.OnPrepare != nil {

		request.OnPrepare(session)

	}

	session.loopDone = make(chan struct{})

	go p.runLoop(session, request)

	return nil

}

// Swap tears down the active stream in the guild without leaving voice, leaving
// the session re-armed (and still Busy) so PlayReusing can start a new stream
// while the worker stays in the call. It returns the re-armed session.
func (p *Pool) Swap(guildID string) (*Session, error) {

	p.mu.Lock()

	session, ok := p.sessions[guildID]

	if !ok {

		p.mu.Unlock()
		return nil, ErrNoWorker

	}

	if !session.Busy {

		p.mu.Unlock()
		return p.Acquire(guildID)

	}

	session.swapping = true
	done := session.loopDone

	p.mu.Unlock()

	if session.controller != nil {

		session.controller()

	}

	if done != nil {

		select {

		case <-done:

		case <-time.After(hotswapTeardownTimeout):

		}

	}

	p.rearmForSwap(session)

	return session, nil

}

func (p *Pool) rearmForSwap(session *Session) {

	p.mu.Lock()
	defer p.mu.Unlock()

	session.Busy = true
	session.Paused = false
	session.StopRequested = false
	session.swapping = false
	session.stats = &source.MediaSourceStats{}

	session.Captions = &captions.Track{}
	session.FontsDir = ""
	session.CaptionSource = ""
	session.CaptionQueryKey = ""

	session.Metadata = nil
	session.ctaFontPath = ""
	session.pendingSegmentCTAs = nil
	session.timedCTAs = nil
	session.creditsTriggerMs = 0
	session.nearEndTriggered = false
	session.liveAttempt = 0

}

// PlayReusing starts a new stream on a session that is already connected to
// voice (after Swap), avoiding a voice re-join so the worker never leaves the call.
func (p *Pool) PlayReusing(ctx context.Context, session *Session, request Request) error {

	if session.Streamer.VoiceConnection() == nil {

		return p.Play(ctx, session, request)

	}

	session.StopRequested = false
	session.request = &request
	session.startedAt = time.Now()
	session.pausedAt = time.Time{}
	session.stats = &source.MediaSourceStats{}

	session.Streamer.SetOnVoiceLeave(func() {

		if !session.Busy {

			return

		}

		if session.controller != nil {

			session.controller()

		}

	})

	session.Metadata = request.Metadata

	if request.OnPrepare != nil {

		request.OnPrepare(session)

	}

	session.loopDone = make(chan struct{})

	go p.runLoop(session, request)

	return nil

}

func (p *Pool) Stop(session *Session) {

	session.StopRequested = true
	session.Paused = false

	if session.controller != nil {

		session.controller()

	}

}

func (p *Pool) Pause(session *Session) {

	if session.Paused || !session.Busy || session.Live() {

		return

	}

	session.Paused = true
	session.pausedAt = time.Now()

	if session.transcodePause != nil {

		session.transcodePause()

	}

}

func (p *Pool) Resume(session *Session) {

	if !session.Paused {

		return

	}

	if !session.startedAt.IsZero() && !session.pausedAt.IsZero() {

		session.startedAt = session.startedAt.Add(time.Since(session.pausedAt))

	}

	session.Paused = false
	session.pausedAt = time.Time{}

	if session.transcodeResume != nil {

		session.transcodeResume()

	}

}

var ErrUnseekable = errors.New("this source cannot be seeked")

func (p *Pool) Seek(session *Session, target time.Duration) (time.Duration, error) {

	session.seekMu.Lock()
	defer session.seekMu.Unlock()

	if !session.Busy || session.request == nil {

		return 0, errors.New("no active stream")

	}

	if session.request.Live {

		return 0, errors.New("live streams cannot be seeked")

	}

	// libav 6.1's HLS demuxer corrupts fMP4 on av_seek_frame; refuse rather than freeze.
	if source.IsHlsURL(session.request.InitialURL) {

		return 0, ErrUnseekable

	}

	if target < 0 {

		target = 0

	}

	if ms := session.stats.DurationMs; ms != nil {

		limit := time.Duration(*ms)*time.Millisecond - 2*time.Second

		if limit < 0 {

			limit = 0

		}

		if target > limit {

			target = limit

		}

	}

	session.pendingSeek = &target

	if session.segmentCancel != nil {

		session.segmentCancel()

	}

	return target, nil

}

func (p *Pool) Release(session *Session) {

	if session.controller != nil {

		session.controller()
		session.controller = nil

	}

	if session.media != nil {

		session.media.Destroy()
		session.media = nil

	}

	session.request = nil
	session.startedAt = time.Time{}
	session.pausedAt = time.Time{}
	session.stats = &source.MediaSourceStats{}
	session.transcodePause = nil
	session.transcodeResume = nil

	session.seekMu.Lock()
	session.pendingSeek = nil
	session.segmentCancel = nil
	session.seekMu.Unlock()

	if session.Captions != nil {

		session.Captions.Reset()

	}

	session.FontsDir = ""
	session.CaptionSource = ""
	session.CaptionQueryKey = ""

	session.Metadata = nil
	session.ctaFontPath = ""
	session.pendingSegmentCTAs = nil
	session.timedCTAs = nil
	session.creditsTriggerMs = 0
	session.nearEndTriggered = false
	session.liveAttempt = 0

	session.Streamer.SetOnVoiceLeave(nil)
	session.Streamer.LeaveVoice()

	session.Busy = false
	session.Paused = false
	session.StopRequested = false

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	// Native heaps (libav, libdatachannel) survive until exit; nudge the runtime after long sessions.
	transcode.TrimNativeHeap()
	runtime.GC()
	debug.FreeOSMemory()

	runtime.ReadMemStats(&after)

	log.Printf("[stream] session %s released: heap in use %s -> %s, reserved %s",
		session.ID,
		formatMem(before.HeapInuse),
		formatMem(after.HeapInuse),
		formatMem(after.Sys))

}

func (p *Pool) runLoop(session *Session, request Request) {

	done := session.loopDone

	reason := CloseEnded

	ctx, cancel := context.WithCancel(context.Background())
	session.controller = cancel

	if request.OnNearEnd != nil {

		go p.monitorPlayback(ctx, session, request)

	}

	playErr := p.stream(ctx, session, request)

	if session.StopRequested {

		reason = CloseStopped

	} else if playErr != nil {

		reason = CloseError
		log.Printf(`[stream] "%s" failed: %v`, request.Caption, playErr)

	}

	cancel()

	p.mu.Lock()
	swapping := session.swapping
	p.mu.Unlock()

	if swapping {

		p.teardownForSwap(session)

		// Notify before unblocking Swap so the swapped-in stream's message edit
		// deterministically lands after this one (they may share a message).
		if request.OnClose != nil {

			request.OnClose(CloseSwapped)

		}

		if done != nil {

			close(done)

		}

		return

	}

	if reason == CloseEnded && request.OnNearEnd != nil && !session.nearEndTriggered {

		request.OnNearEnd()

	}

	p.Release(session)

	if done != nil {

		close(done)

	}

	if request.OnClose != nil {

		request.OnClose(reason)

	}

}

// teardownForSwap releases the playback pipeline but keeps the voice connection
// alive so a swapped-in stream resumes without the worker leaving the call.
func (p *Pool) teardownForSwap(session *Session) {

	session.controller = nil

	if session.media != nil {

		session.media.Destroy()
		session.media = nil

	}

	session.seekMu.Lock()
	session.pendingSeek = nil
	session.segmentCancel = nil
	session.seekMu.Unlock()

}

func (p *Pool) stream(ctx context.Context, session *Session, request Request) error {

	headers := request.Headers

	if headers == nil {

		headers = config.FebboxStreamHeaders()

	}

	playback, err := streamer.OpenPlayback(ctx, session.Streamer)

	if err != nil {

		return err

	}

	defer playback.Close()

	if source.IsHlsURL(request.InitialURL) {

		return p.playHLS(ctx, session, playback, request, headers)

	}

	return p.playProgressive(ctx, session, playback, request, headers)

}

func (p *Pool) playProgressive(ctx context.Context, session *Session, playback *streamer.Playback, request Request, headers map[string]string) error {

	offset := time.Duration(0)
	qualityAttempt := 0

	for {

		if err := ctx.Err(); err != nil {

			return err

		}

		playbackURL, err := playbackURLForAttempt(request, qualityAttempt)

		if err != nil {

			return err

		}

		media, err := source.Create(request.ResolveURL, headers, playbackURL, session.stats)

		if err != nil {

			return err

		}

		session.media = media

		treq := transcode.Request{

			Source: media,
			Caption: request.Caption,

		}

		session.enrichTranscodeRequest(&treq, offset)

		playErr, transErr := p.runSegment(ctx, session, playback, treq, offset, media.Destroy)

		session.media = nil

		if target, ok := session.takeSeek(); ok && ctx.Err() == nil {

			offset = target
			continue

		}

		if ctx.Err() != nil {

			return ctx.Err()

		}

		if playErr != nil {

			return playErr

		}

		if transErr != nil && request.QualityURL != nil {

			if _, err := request.QualityURL(qualityAttempt + 1); err == nil {

				log.Printf(`[stream] transcode failed for "%s", trying quality fallback %d: %v`,
					request.Caption, qualityAttempt+1, transErr)
				qualityAttempt++
				offset = 0
				continue

			}

		}

		return transErr

	}

}

func playbackURLForAttempt(request Request, attempt int) (string, error) {

	if request.QualityURL != nil {

		url, err := request.QualityURL(attempt)

		if err != nil {

			return "", err

		}

		if url != "" {

			return url, nil

		}

	}

	if attempt == 0 {

		return request.InitialURL, nil

	}

	return "", fmt.Errorf("no quality fallback for attempt %d", attempt)

}

const (
	hlsStartupRetryWindow = 15 * time.Second
	liveReconnectDelayMin = 2 * time.Second
	liveReconnectDelayMax = 15 * time.Second
	liveResolveRetries = 3
	liveStableSegmentWindow = 30 * time.Second

	// liveStartupTimeout is how long to wait for the HLS playlist to open before forcing a reconnect.
	// Must cover open retries plus first-segment fetch on slow live CDNs.
	liveStartupTimeout = 20 * time.Second
	// liveProviderFallbackAfter is the number of consecutive failures before trying the fallback provider.
	liveProviderFallbackAfter = 3
)

func (p *Pool) playHLS(ctx context.Context, session *Session, playback *streamer.Playback, request Request, headers map[string]string) error {

	if request.Live {

		return p.playLiveHLS(ctx, session, playback, request, headers)

	}

	return p.playVodHLS(ctx, session, playback, request, headers)

}

func (p *Pool) playVodHLS(ctx context.Context, session *Session, playback *streamer.Playback, request Request, headers map[string]string) error {

	url := request.InitialURL
	var lastErr error

	for attempt := 0; attempt <= config.Download.MaxRetries; attempt++ {

		if ctx.Err() != nil {

			return ctx.Err()

		}

		if attempt > 0 {

			log.Printf(`[stream] HLS retry %d/%d for "%s"`, attempt, config.Download.MaxRetries, request.Caption)

			if request.ResolveURL != nil {

				if fresh, err := request.ResolveURL(); err == nil && fresh != "" {

					url = fresh

				}

			}

			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)

		}

		started := time.Now()

		treq := transcode.Request{

			InputURL: url,
			Headers: headers,
			Caption: request.Caption,

		}

		session.enrichTranscodeRequest(&treq, 0)

		playErr, transErr := p.runSegment(ctx, session, playback, treq, 0, nil)

		if ctx.Err() != nil {

			return ctx.Err()

		}

		if playErr == nil && transErr == nil {

			return nil

		}

		if playErr != nil {

			lastErr = playErr

		} else {

			lastErr = transErr

		}

		if time.Since(started) >= hlsStartupRetryWindow || attempt >= config.Download.MaxRetries {

			return lastErr

		}

	}

	return lastErr

}

func liveFailureCountAfterSegment(consecutiveFailures int, segmentDuration time.Duration, cleanEnd bool) int {

	if segmentDuration >= liveStableSegmentWindow {

		return 0

	}

	if cleanEnd {

		return consecutiveFailures

	}

	return consecutiveFailures + 1

}

func liveReconnectDelay(consecutiveFailures int) time.Duration {

	if consecutiveFailures <= 1 {

		return liveReconnectDelayMin

	}

	shift := consecutiveFailures - 1

	if shift > 3 {

		shift = 3

	}

	delay := liveReconnectDelayMin * time.Duration(1<<shift)

	if delay > liveReconnectDelayMax {

		return liveReconnectDelayMax

	}

	return delay

}

func refreshLiveUpstream(ctx context.Context, request Request, currentURL string, currentHeaders map[string]string) (string, map[string]string) {

	url := currentURL
	headers := currentHeaders

	for retry := 0; retry < liveResolveRetries; retry++ {

		if ctx.Err() != nil {

			return url, headers

		}

		if request.ResolveURL != nil {

			fresh, err := request.ResolveURL()

			if err == nil && fresh != "" {

				url = fresh

			} else if err != nil {

				log.Printf(`[stream] live URL re-resolve failed for "%s" (attempt %d/%d): %v`, request.Caption, retry+1, liveResolveRetries, err)

			}

		}

		if request.ResolveHeaders != nil {

			if fresh := request.ResolveHeaders(); len(fresh) > 0 {

				headers = fresh

			}

		}

		if request.ResolveURL == nil || url != "" {

			return url, headers

		}

		select {

		case <-ctx.Done():

			return url, headers

		case <-time.After(500 * time.Millisecond):

		}

	}

	return url, headers

}

func (p *Pool) playLiveHLS(ctx context.Context, session *Session, playback *streamer.Playback, request Request, headers map[string]string) error {

	url := request.InitialURL
	attempt := 0
	consecutiveFailures := 0
	usingFallback := false

	for {

		if ctx.Err() != nil {

			return ctx.Err()

		}

		if attempt > 0 {

			// Switch to the fallback provider after enough consecutive failures.
			if !usingFallback && consecutiveFailures >= liveProviderFallbackAfter && request.ResolveFallbackURL != nil {

				log.Printf(`[stream] switching to fallback provider for "%s" after %d consecutive failures`, request.Caption, consecutiveFailures)
				usingFallback = true

			}

			log.Printf(`[stream] live reconnect %d for "%s" (fallback=%v)`, attempt, request.Caption, usingFallback)

			if usingFallback {

				url, headers = refreshFallbackUpstream(ctx, request, url, headers)

			} else {

				url, headers = refreshLiveUpstream(ctx, request, url, headers)

			}

			if url == "" {

				log.Printf(`[stream] live reconnect for "%s" has no URL; retrying upstream resolve`, request.Caption)
				consecutiveFailures++
				attempt++

				select {

				case <-ctx.Done():

					return ctx.Err()

				case <-time.After(liveReconnectDelay(consecutiveFailures)):

				}

				continue

			}

			select {

			case <-ctx.Done():

				return ctx.Err()

			case <-time.After(liveReconnectDelay(consecutiveFailures)):

			}

		}

		segmentStart := time.Now()

		session.liveAttempt = attempt

		if attempt == 0 {

			session.pendingSegmentCTAs = nil

		} else if consecutiveFailures > 1 {

			session.pendingSegmentCTAs = []SegmentCTA{{Text: "The live stream provider is degraded", DurationMs: liveCTADurationMs}}

		} else {

			session.pendingSegmentCTAs = []SegmentCTA{{Text: "The live stream has restarted", DurationMs: liveCTADurationMs}}

		}

		treq := transcode.Request{

			InputURL: url,
			Headers: headers,
			Caption: request.Caption,
			Live: true,

		}

		session.enrichTranscodeRequest(&treq, 0)

		playErr, transErr := p.runSegment(ctx, session, playback, treq, 0, nil)

		if ctx.Err() != nil {

			return ctx.Err()

		}

		cleanEnd := playErr == nil && transErr == nil

		if cleanEnd {

			log.Printf(`[stream] live source ended for "%s", reconnecting`, request.Caption)

		} else {

			err := playErr

			if err == nil {

				err = transErr

			}

			log.Printf(`[stream] live stream "%s" dropped: %v`, request.Caption, err)

		}

		consecutiveFailures = liveFailureCountAfterSegment(consecutiveFailures, time.Since(segmentStart), cleanEnd)

		attempt++

	}

}

func refreshFallbackUpstream(ctx context.Context, request Request, currentURL string, currentHeaders map[string]string) (string, map[string]string) {

	url := currentURL
	headers := currentHeaders

	if request.ResolveFallbackURL != nil {

		fresh, err := request.ResolveFallbackURL()

		if err == nil && fresh != "" {

			url = fresh

		} else if err != nil {

			log.Printf(`[stream] fallback URL resolve failed: %v`, err)

		}

	}

	if request.ResolveFallbackHeaders != nil {

		if fresh := request.ResolveFallbackHeaders(); len(fresh) > 0 {

			headers = fresh

		}

	}

	return url, headers

}

func (p *Pool) runSegment(ctx context.Context, session *Session, playback *streamer.Playback, treq transcode.Request, offset time.Duration, cleanup func()) (error, error) {

	segCtx, segCancel := context.WithCancel(ctx)
	defer segCancel()

	session.setSegmentCancel(segCancel)

	// A seek that raced in between segments cancelled a stale context; honor it now.
	if session.seekPending() {

		segCancel()

	}

	// For live streams, set up a startup watchdog that fires if the HLS playlist
	// open stalls (SupplyCTAs not called) within liveStartupTimeout.
	var streamOpened chan struct{}

	if treq.Live {

		streamOpened = make(chan struct{}, 1)

	}

	treq.Start = offset
	treq.Context = segCtx
	treq.SupplyCTAs = func(probedDurationMs int64, startMs int64) (string, []transcode.CTAWindow) {

		if streamOpened != nil {

			select {

			case streamOpened <- struct{}{}:
			default:

			}

		}

		if probedDurationMs > 0 {

			session.stats.DurationMs = &probedDurationMs

			if session.request != nil && session.request.OnMediaProbed != nil {

				session.request.OnMediaProbed(session, probedDurationMs)

			}

			session.armCreditsTrigger(probedDurationMs)

		}

		offset := time.Duration(startMs) * time.Millisecond
		windows := session.buildCTAWindows(offset)
		fontPath := session.ctaFontPath

		if fontPath == "" {

			return "", windows

		}

		return fontPath, windows

	}

	if session.Captions != nil && session.Captions.Enabled() {

		treq.SubtitlePath = session.Captions.Path()
		treq.FontsDir = session.FontsDir

	}

	ts, err := transcode.Start(treq)

	if err != nil {

		if cleanup != nil {

			cleanup()

		}

		return err, nil

	}

	if treq.Live {

		go func() {

			select {

			case <-streamOpened:
			case <-time.After(liveStartupTimeout):
				log.Printf(`[stream] live stream open timeout for "%s", forcing reconnect`, treq.Caption)
				segCancel()
			case <-segCtx.Done():

			}

		}()

	}

	session.transcodePause = ts.Pause
	session.transcodeResume = ts.Resume
	session.startedAt = time.Now().Add(-offset)

	if session.Paused {

		session.pausedAt = time.Now()
		ts.Pause()

	}

	playErr := playback.Run(segCtx, ts, segCancel)

	// Unblock in-flight reads for teardown; don't cancel segCtx yet or ts.Done gets ctx.Canceled.
	if cleanup != nil {

		cleanup()

	}

	// The packet feeds close on EOF without surfacing libav errors; ts.Done carries them.
	var transErr error

	select {

	case transErr = <-ts.Done:

	case <-time.After(5 * time.Second):

	}

	return playErr, transErr

}

func (p *Pool) SetKey(ctx context.Context, guildID, token string) error {

	p.mu.Lock()

	if session, ok := p.sessions[guildID]; ok && session.Busy {

		p.mu.Unlock()
		return ErrKeyChangeActive

	}

	p.mu.Unlock()

	client, err := selfbot.NewClient(token)

	if err != nil {

		return err

	}

	if err := client.Login(ctx); err != nil {

		return err

	}

	if err := p.store.Set(guildID, token); err != nil {

		return err

	}

	p.mu.Lock()

	if existing := p.sessions[guildID]; existing != nil {

		existing.Streamer.SetOnVoiceLeave(nil)
		existing.Streamer.LeaveVoice()

	}

	stream := streamer.New(client)
	p.sessions[guildID] = &Session{

		ID: guildID,
		Client: client,
		Streamer: stream,

		stats: &source.MediaSourceStats{},

	}
	p.mu.Unlock()

	return nil

}

func (p *Pool) addWorker(ctx context.Context, guildID, token string) error {

	client, err := selfbot.NewClient(token)

	if err != nil {

		return err

	}

	if err := client.Login(ctx); err != nil {

		return err

	}

	stream := streamer.New(client)

	p.mu.Lock()
	p.sessions[guildID] = &Session{

		ID: guildID,
		Client: client,
		Streamer: stream,

		stats: &source.MediaSourceStats{},

	}
	p.mu.Unlock()

	return nil

}

func captionOf(session *Session) string {

	if session.request == nil {

		return ""

	}

	return session.request.Caption

}

func channelOf(session *Session) string {

	if session.request == nil {

		return ""

	}

	return session.request.ChannelID

}

func qualityOf(session *Session) string {

	if session.request == nil {

		return ""

	}

	return session.request.QualityLabel

}

func min64(a, b int64) int64 {

	if a < b {

		return a

	}

	return b

}

func formatMem(bytes uint64) string {

	const unit = 1024

	if bytes < unit {

		return fmt.Sprintf("%d B", bytes)

	}

	value := float64(bytes)
	exp := 0

	for value >= unit && exp < 4 {

		value /= unit
		exp++

	}

	suffix := []string{"KiB", "MiB", "GiB", "TiB"}

	return fmt.Sprintf("%.1f %s", value, suffix[exp-1])

}

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
	ErrNoWorker        = errors.New("No worker is configured for your server.")
	ErrWorkerBusy      = errors.New("A stream is already active in this server.")
	ErrKeyChangeActive = errors.New("cannot change key while a stream is active")
)

// CloseReason is why a playback loop ended.
type CloseReason string

const (
	CloseEnded   CloseReason = "ended"
	CloseStopped CloseReason = "stopped"
	CloseError   CloseReason = "error"
)

// QualityResolver returns the playback URL for attempt 0 (primary) and higher fallbacks.
type QualityResolver func(attempt int) (string, error)

// Request is everything a session needs to download, transcode, and play one title.
type Request struct {
	GuildID        string
	ChannelID      string
	Caption        string // Log tag and stats label.
	InitialURL     string
	ResolveURL     source.UrlResolver
	QualityURL     QualityResolver // Optional Febbox quality fallbacks when transcode fails.
	QualityLabel   string
	Headers        map[string]string        // HTTP headers for HLS/direct input; defaults to Febbox when nil.
	ResolveHeaders func() map[string]string // Optional live-TV header refresh on reconnect.
	Live           bool                     // Live streams cannot be paused and re-resolve on expiry.
	Metadata       *StreamMetadata          // Optional VOD/live context for handlers and hooks.
	OnPrepare      func(*Session)             // Called before playback; warms captions and intro timing.
	OnMediaProbed  func(*Session, int64)      // Called when container duration is known, before the filter graph is built.
	OnNearEnd      func()                     // Called once when credits begin on a TV episode.
	OnClose        func(CloseReason)
}

// Session is a single selfbot account streaming to one voice channel at a time.
type Session struct {
	ID       string
	Streamer *streamer.Streamer
	Client   *selfbot.Client

	Busy          bool
	Paused        bool // Playback state; pausing stalls libav reads and frame pacing.
	StopRequested bool

	controller context.CancelFunc
	media      *source.MediaSource
	request    *Request
	startedAt  time.Time
	pausedAt   time.Time
	stats      *source.MediaSourceStats

	transcodePause  func()
	transcodeResume func()

	seekMu        sync.Mutex
	pendingSeek   *time.Duration
	segmentCancel context.CancelFunc

	Captions        *captions.Track
	FontsDir        string
	CaptionSource   string
	CaptionQueryKey string

	Metadata           *StreamMetadata
	ctaFontPath        string
	pendingSegmentCTAs []SegmentCTA
	timedCTAs          []TimedCTA
	creditsTriggerMs   int64
	nearEndTriggered   bool
}

func (session *Session) setSegmentCancel(cancel context.CancelFunc) {

	session.seekMu.Lock()
	defer session.seekMu.Unlock()

	session.segmentCancel = cancel

}

// takeSeek consumes a pending seek target, if any.
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

// Stats is a user-facing playback snapshot.
type Stats struct {
	ID              string
	Caption         string
	ChannelID       string
	Paused          bool
	CaptionsEnabled bool
	CaptionSource   string
	UptimeMs        int64
	BytesRead       int64
	QualityLabel    string
	PositionMs      int64
	DurationMs      *int64
}

// Pool owns one selfbot worker per guild and runs each download → transcode → play loop.
type Pool struct {
	mu       sync.Mutex
	sessions map[string]*Session
	store    *workers.Store
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

	return session, nil

}

func (p *Pool) Get(id string) *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.sessions[id]

}

// Live reports whether the session is streaming a live source that cannot be paused.
func (session *Session) Live() bool {

	if session.request == nil {
		return false
	}

	return session.request.Live

}

// ActiveInGuild returns the busy stream session in the guild, if any.
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
		ID:              session.ID,
		Caption:         captionOf(session),
		ChannelID:       channelOf(session),
		Paused:          session.Paused,
		CaptionsEnabled: session.Captions != nil && session.Captions.Enabled(),
		CaptionSource:   session.CaptionSource,
		UptimeMs:        uptime,
		BytesRead:       session.stats.BytesRead,
		QualityLabel:    qualityOf(session),
		PositionMs:      position,
		DurationMs:      session.stats.DurationMs,
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

// ErrUnseekable marks sources whose container cannot be repositioned safely.
var ErrUnseekable = errors.New("this source cannot be seeked")

// Seek schedules a jump to target; playback restarts transcode while the Discord stream stays up.
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

	session.Streamer.SetOnVoiceLeave(nil)
	session.Streamer.LeaveVoice()

	session.Busy = false
	session.Paused = false
	session.StopRequested = false

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	// Encourage the runtime to return pages after a long transcode session. Native heaps
	// (libav, libdatachannel) are not released until process exit.
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

	if reason == CloseEnded && request.OnNearEnd != nil && !session.nearEndTriggered {
		request.OnNearEnd()
	}

	p.Release(session)

	if request.OnClose != nil {
		request.OnClose(reason)
	}

}

// stream downloads, transcodes, and plays one title; one Go Live stream stays open for /seek.
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
			Source:  media,
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
	hlsStartupRetryWindow   = 15 * time.Second
	liveReconnectDelayMin   = 2 * time.Second
	liveReconnectDelayMax   = 15 * time.Second
	liveResolveRetries      = 3
	liveStableSegmentWindow = 30 * time.Second
)

// playHLS never seeks: Seek refuses HLS sources outright (broken libav fMP4 seek).
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
			Headers:  headers,
			Caption:  request.Caption,
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

// liveFailureCountAfterSegment updates the reconnect backoff counter from one live segment result.
func liveFailureCountAfterSegment(consecutiveFailures int, segmentDuration time.Duration, cleanEnd bool) int {

	if segmentDuration >= liveStableSegmentWindow {
		return 0
	}

	if cleanEnd {
		return consecutiveFailures
	}

	return consecutiveFailures + 1

}

// liveReconnectDelay backs off briefly on repeated upstream failures without delaying healthy drops.
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

// refreshLiveUpstream re-resolves the HLS URL and headers before a reconnect attempt.
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

// prepareLiveHLSInput wraps obfuscated live playlists in a localhost TS-only relay for libav.
func prepareLiveHLSInput(ctx context.Context, upstream string, headers map[string]string) (playbackURL string, playbackHeaders map[string]string, stop func()) {

	relay, err := source.StartLiveHLSRelay(ctx, upstream, headers)

	if err != nil {
		log.Printf("[stream] live hls relay unavailable, using upstream: %v", err)
		return upstream, headers, func() {}
	}

	return relay.URL(), nil, relay.Close

}

// playLiveHLS keeps the Discord stream open and reconnects to the upstream source on drop.
func (p *Pool) playLiveHLS(ctx context.Context, session *Session, playback *streamer.Playback, request Request, headers map[string]string) error {

	url := request.InitialURL
	attempt := 0
	consecutiveFailures := 0

	for {

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if attempt > 0 {

			log.Printf(`[stream] live reconnect %d for "%s"`, attempt, request.Caption)

			url, headers = refreshLiveUpstream(ctx, request, url, headers)

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

		if attempt == 0 {
			session.pendingSegmentCTAs = nil
		} else if consecutiveFailures > 1 {
			session.pendingSegmentCTAs = []SegmentCTA{{Text: "The live stream provider is degraded", DurationMs: liveCTADurationMs}}
		} else {
			session.pendingSegmentCTAs = []SegmentCTA{{Text: "The live stream has restarted", DurationMs: liveCTADurationMs}}
		}

		playbackURL, playbackHeaders, stopRelay := prepareLiveHLSInput(ctx, url, headers)
		defer stopRelay()

		treq := transcode.Request{
			InputURL: playbackURL,
			Headers:  playbackHeaders,
			Caption:  request.Caption,
			Live:     true,
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

// runSegment plays one transcode session into the open stream, starting at offset.
func (p *Pool) runSegment(ctx context.Context, session *Session, playback *streamer.Playback, treq transcode.Request, offset time.Duration, cleanup func()) (error, error) {

	segCtx, segCancel := context.WithCancel(ctx)
	defer segCancel()

	session.setSegmentCancel(segCancel)

	// A seek that raced in between segments cancelled a stale context; honor it now.
	if session.seekPending() {
		segCancel()
	}

	treq.Start = offset
	treq.Context = segCtx
	treq.SupplyCTAs = func(probedDurationMs int64, startMs int64) (string, []transcode.CTAWindow) {

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

	session.transcodePause = ts.Pause
	session.transcodeResume = ts.Resume
	session.startedAt = time.Now().Add(-offset)

	if session.Paused {
		session.pausedAt = time.Now()
		ts.Pause()
	}

	playErr := playback.Run(segCtx, ts)

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
	p.sessions[guildID] = &Session{ID: guildID, Client: client, Streamer: stream, stats: &source.MediaSourceStats{}}
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
	p.sessions[guildID] = &Session{ID: guildID, Client: client, Streamer: stream, stats: &source.MediaSourceStats{}}
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

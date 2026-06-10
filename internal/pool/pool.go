package pool

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"streamly/internal/config"
	"streamly/internal/selfbot"
	"streamly/internal/source"
	"streamly/internal/streamer"
	"streamly/internal/transcode"
)

// CloseReason is why a playback loop ended.
type CloseReason string

const (
	CloseEnded   CloseReason = "ended"
	CloseStopped CloseReason = "stopped"
	CloseError   CloseReason = "error"
)

// Request is everything a session needs to download, transcode, and play one title.
type Request struct {
	GuildID      string
	ChannelID    string
	Caption      string // Bottom-right overlay label and log tag.
	InitialURL   string
	ResolveURL   source.UrlResolver
	QualityLabel string
	Headers      map[string]string // HTTP headers for HLS/direct input; defaults to Febbox when nil.
	Live         bool              // Live streams cannot be paused and re-resolve on expiry.
	OnClose      func(CloseReason)
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
}

// Stats is a user-facing playback snapshot.
type Stats struct {
	ID           string
	Caption      string
	ChannelID    string
	Paused       bool
	UptimeMs     int64
	BytesRead    int64
	QualityLabel string
	PositionMs   int64
	DurationMs   *int64
}

// Pool owns the streaming accounts and runs each download → transcode → play loop.
type Pool struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func New() *Pool {

	return &Pool{sessions: make(map[string]*Session)}

}

func (p *Pool) Size() int {

	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.sessions)

}

func (p *Pool) Login(ctx context.Context, tokens []string) error {

	for index, token := range tokens {
		if err := p.add(ctx, fmt.Sprintf("slot-%d", index), token); err != nil {
			log.Printf("streaming account slot-%d failed to log in: %v", index, err)
		}
	}

	return nil

}

func (p *Pool) Acquire() *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, session := range p.sessions {

		if !session.Busy {

			session.Busy = true
			session.Paused = false
			session.StopRequested = false
			session.stats = &source.MediaSourceStats{}

			return session

		}

	}

	return nil

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

// ActiveInGuild returns any busy stream session in the guild.
func (p *Pool) ActiveInGuild(guildID string) *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, session := range p.sessions {

		if session.Busy && session.request != nil && session.request.GuildID == guildID {
			return session
		}

	}

	return nil

}

func (p *Pool) Active(guildID, channelID string) *Session {

	p.mu.Lock()
	defer p.mu.Unlock()

	var matches []*Session

	for _, session := range p.sessions {

		if session.Busy && session.request != nil && session.request.GuildID == guildID {
			matches = append(matches, session)
		}

	}

	for _, session := range matches {
		if session.request.ChannelID == channelID {
			return session
		}
	}

	if len(matches) > 0 {
		return matches[0]
	}

	return nil

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
		ID:           session.ID,
		Caption:      captionOf(session),
		ChannelID:    channelOf(session),
		Paused:       session.Paused,
		UptimeMs:     uptime,
		BytesRead:    session.stats.BytesRead,
		QualityLabel: qualityOf(session),
		PositionMs:   position,
		DurationMs:   session.stats.DurationMs,
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

	if session.Paused || !session.Busy {
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

	session.Streamer.LeaveVoice()

	session.Busy = false
	session.Paused = false
	session.StopRequested = false

}

func (p *Pool) runLoop(session *Session, request Request) {

	reason := CloseEnded

	ctx, cancel := context.WithCancel(context.Background())
	session.controller = cancel

	playErr := p.stream(ctx, session, request)

	if session.StopRequested {
		reason = CloseStopped
	} else if playErr != nil {
		reason = CloseError
		log.Printf(`[stream] "%s" failed: %v`, request.Caption, playErr)
	}

	cancel()
	p.Release(session)

	if request.OnClose != nil {
		request.OnClose(reason)
	}

}

// stream downloads, transcodes, and plays one title; it blocks until playback ends.
func (p *Pool) stream(ctx context.Context, session *Session, request Request) error {

	headers := request.Headers

	if headers == nil {
		headers = config.FebboxStreamHeaders()
	}

	if source.IsHlsURL(request.InitialURL) {
		return p.playHLS(ctx, session, request, headers)
	}

	media, err := source.Create(request.ResolveURL, headers, request.InitialURL, session.stats)

	if err != nil {
		return err
	}

	session.media = media

	ts, err := transcode.Start(transcode.Request{
		Source:  media.Stream,
		Caption: request.Caption,
		Key:     session.ID,
		Context: ctx,
	})

	if err != nil {
		media.Destroy()
		return err
	}

	session.transcodePause = ts.Pause
	session.transcodeResume = ts.Resume

	if session.Paused {
		ts.Pause()
	}

	playErr := streamer.Play(ctx, session.Streamer, ts)

	// The packet feeds close on EOF without surfacing libav errors; ts.Done carries them.
	var transErr error

	select {
	case transErr = <-ts.Done:
	case <-time.After(5 * time.Second):
	}

	media.Destroy()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if playErr != nil {
		return playErr
	}

	return transErr

}

const hlsStartupRetryWindow = 15 * time.Second

func (p *Pool) playHLS(ctx context.Context, session *Session, request Request, headers map[string]string) error {

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

		ts, err := transcode.Start(transcode.Request{
			InputURL: url,
			Headers:  headers,
			Caption:  request.Caption,
			Key:      session.ID,
			Context:  ctx,
		})

		if err != nil {
			lastErr = err

			if attempt < config.Download.MaxRetries {
				continue
			}

			return err
		}

		session.transcodePause = ts.Pause
		session.transcodeResume = ts.Resume

		if session.Paused {
			ts.Pause()
		}

		playErr := streamer.Play(ctx, session.Streamer, ts)

		var transErr error

		select {
		case transErr = <-ts.Done:
		case <-time.After(5 * time.Second):
		}

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
			if playErr != nil {
				return playErr
			}

			return transErr
		}

	}

	if lastErr != nil {
		return lastErr
	}

	return nil

}

func (p *Pool) add(ctx context.Context, id, token string) error {

	client, err := selfbot.NewClient(token)

	if err != nil {
		return err
	}

	if err := client.Login(ctx); err != nil {
		return err
	}

	s := streamer.New(client)

	p.mu.Lock()
	p.sessions[id] = &Session{ID: id, Client: client, Streamer: s, stats: &source.MediaSourceStats{}}
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

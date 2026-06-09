package transcode

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"

	"streamly/internal/config"
)

// Kind discriminates the two elementary streams the transcoder emits.
type Kind int

const (
	KindVideo Kind = iota
	KindAudio
)

// Packet is one encoded elementary stream packet: Annex-B H264 or raw Opus.
type Packet struct {
	Kind     Kind
	Data     []byte
	PTS      time.Duration
	Duration time.Duration
}

// Request describes one libav transcode job fed from in-process media readers.
type Request struct {
	Source   io.Reader // Progressive media, muxed audio+video.
	InputURL string    // Direct media URL; HLS uses libavformat's in-process demuxer.
	Headers  map[string]string
	Caption  string // Bottom-right label burned in when overlay assets exist.
	Key      string // Unique per playback; names scratch caption files.
	Context  context.Context
}

// Session is a running transcode: encoded video/audio feeds, completion state, and pause control.
type Session struct {
	Video <-chan Packet
	Audio <-chan Packet
	Done  <-chan error

	pause *pauseState
}

// Pause stops reading from inputs; backpressure stalls the libav pipeline without signals.
func (s *Session) Pause() {

	if s.pause != nil {
		s.pause.Pause()
	}

}

// Resume continues reading from inputs after Pause.
func (s *Session) Resume() {

	if s.pause != nil {
		s.pause.Resume()
	}

}

func (s *Session) WaitIfPaused(ctx context.Context) bool {

	if s.pause == nil {
		return true
	}

	return s.pause.Wait(ctx)

}

func (s *Session) PauseEvent(lastSeen uint64) (time.Duration, uint64) {

	if s.pause == nil {
		return 0, lastSeen
	}

	return s.pause.Event(lastSeen)

}

type pauseState struct {
	mu     sync.Mutex
	paused bool
	since  time.Time
	last   time.Duration
	epoch  uint64
}

func newPauseState() *pauseState {

	return &pauseState{}

}

func (p *pauseState) Pause() {

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.paused {
		return
	}

	p.paused = true
	p.since = time.Now()

}

func (p *pauseState) Resume() {

	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.paused {
		return
	}

	p.last = time.Since(p.since)
	p.epoch++
	p.since = time.Time{}
	p.paused = false

}

func (p *pauseState) Wait(ctx context.Context) bool {

	for {

		p.mu.Lock()
		paused := p.paused
		p.mu.Unlock()

		if !paused {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(20 * time.Millisecond):
		}

	}

}

func (p *pauseState) Event(lastSeen uint64) (time.Duration, uint64) {

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.epoch == lastSeen {
		return 0, lastSeen
	}

	return p.last, p.epoch

}

// Start launches a libav transcode that emits encoded H264 and Opus packets directly to Go.
func Start(request Request) (*Session, error) {

	if request.Context == nil {
		return nil, fmt.Errorf("transcode request missing context")
	}

	if request.Source == nil && request.InputURL == "" {
		return nil, fmt.Errorf("transcode request missing source")
	}

	if err := request.Context.Err(); err != nil {
		return nil, err
	}

	label := request.Caption

	if label == "" {
		label = "(no caption)"
	}

	if request.InputURL != "" {
		log.Printf("[transcode] libav started %q (direct input)", label)
	} else {
		log.Printf("[transcode] libav started %q", label)
	}

	return startNative(request)

}

// overlayAvailable mirrors the TS helper: skip overlay when logo or font is missing.
func overlayAvailable() bool {

	if _, err := os.Stat(config.Overlay.LogoPath); err != nil {
		return false
	}

	if _, err := os.Stat(config.Overlay.FontPath); err != nil {
		return false
	}

	return true

}

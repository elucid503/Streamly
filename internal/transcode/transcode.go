package transcode

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

type Kind int

const (
	KindVideo Kind = iota
	KindAudio
)

type Packet struct {

	Kind Kind
	Data []byte
	PTS time.Duration
	Duration time.Duration
}

type InputReader interface {

	io.ReadSeeker
	Size() int64
}

type CTAWindow struct {

	Text string
	StartMs int64
	EndMs int64
}

type PauseCard struct {

	Title string
	Subtitle string
	BodyLines []string

	CTA string
	FontPath string

}

type Request struct {

	Source InputReader
	InputURL string
	Headers map[string]string
	Start time.Duration
	Live bool
	Caption string

	SubtitlePath string
	FontsDir string
	CTAFontPath string
	CTAs []CTAWindow
	PauseCard *PauseCard

	Context context.Context

	OnDuration func(durationMs int64)
	SupplyCTAs func(probedDurationMs int64, startMs int64) (fontPath string, windows []CTAWindow)
}

type pauseFrameEncoder interface {

	encodePauseFrame(card *PauseCard, targetPTSMs int64) ([]byte, error)
}

type Session struct {

	Video <-chan Packet
	Audio <-chan Packet
	Done <-chan error

	pause *pauseState
	card *PauseCard
	encoder pauseFrameEncoder

	frameMu sync.Mutex
	frameEpoch uint64
	frameData []byte
	frameBad bool

}

// Pause stops reading from inputs; backpressure stalls the libav pipeline without signals.
func (s *Session) Pause() {

	if s.pause != nil {

		s.pause.Pause()
	}

}

func (s *Session) Resume() {

	if s.pause != nil {

		s.pause.Resume()
	}

}

func (s *Session) IsPaused() bool {

	if s.pause == nil {

		return false
	}

	paused, _ := s.pause.snapshot()

	return paused

}

func (s *Session) PauseFrame(targetPTSMs int64) ([]byte, bool) {

	if s.pause == nil || s.card == nil || s.encoder == nil {

		return nil, false
	}

	paused, epoch := s.pause.snapshot()

	if !paused {

		return nil, false
	}

	return s.frame(targetPTSMs, epoch)

}

func (s *Session) LoadingFrame(targetPTSMs int64) ([]byte, bool) {

	if s.card == nil || s.encoder == nil {

		return nil, false
	}

	return s.frame(targetPTSMs, 0)

}

func (s *Session) frame(targetPTSMs int64, epoch uint64) ([]byte, bool) {

	s.frameMu.Lock()
	defer s.frameMu.Unlock()

	if s.frameEpoch == epoch && (s.frameData != nil || s.frameBad) {

		return s.frameData, s.frameData != nil
	}

	data, err := s.encoder.encodePauseFrame(s.card, targetPTSMs)

	s.frameEpoch = epoch
	s.frameData = data
	s.frameBad = err != nil

	if err != nil {

		log.Printf("[transcode] pause card render failed: %v", err)
		return nil, false
	}

	return data, true

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

	mu sync.Mutex
	paused bool
	since time.Time
	last time.Duration
	epoch uint64
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

func (p *pauseState) snapshot() (bool, uint64) {

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.paused, p.epoch

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

func TrimNativeHeap() {

	trimNativeHeap()
}

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

		log.Printf("[transcode] libav started %q (direct input) at %s", label, request.Start)
	} else {

		log.Printf("[transcode] libav started %q at %s", label, request.Start)
	}

	return startNative(request)

}

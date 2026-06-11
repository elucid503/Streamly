package transcode

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
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

// InputReader is a byte-seekable media input; Size returns total bytes or -1 when unknown.
// Byte seeks let libavformat use the container index, which is what makes /seek fast.
type InputReader interface {
	io.ReadSeeker
	Size() int64
}

// CTAWindow is a timed on-stream callout burned in at the bottom-left via drawtext.
type CTAWindow struct {
	Text    string
	StartMs int64
	EndMs   int64
}

// PauseCard is the on-stream pause screen content, composed over the frozen frame.
type PauseCard struct {
	Title     string
	Subtitle  string   // "Season X - Episode Y" line; empty for movies.
	BodyLines []string // Pre-wrapped description; at most pauseCardBodyLines are drawn.
	CTA       string
	FontPath  string
}

// Request describes one libav transcode job fed from in-process media readers.
type Request struct {
	Source       InputReader // Progressive media, muxed audio+video.
	InputURL     string      // Direct media URL; HLS uses libavformat's in-process demuxer.
	Headers      map[string]string
	Start        time.Duration // Initial playback position; 0 plays from the beginning.
	Live         bool          // Live HLS: low-latency libav tuning and wider packet queues.
	Caption      string        // Log tag and stats label.
	SubtitlePath string        // External subtitle file for burn-in; empty disables captions.
	FontsDir     string        // Directory containing font.ttf for libass.
	CTAFontPath  string        // Font file for drawtext overlays; empty disables CTAs.
	CTAs         []CTAWindow   // Timed bottom-left callouts for this segment.
	PauseCard    *PauseCard    // On-stream pause screen content; nil disables the pause card.
	Context      context.Context

	OnDuration func(durationMs int64) // Called once when the container duration is known.

	// SupplyCTAs builds drawtext overlays after probing, immediately before the filter graph is built.
	SupplyCTAs func(probedDurationMs int64, startMs int64) (fontPath string, windows []CTAWindow)
}

// pauseFrameEncoder renders the pause card into one encoded IDR frame; cgo-only.
type pauseFrameEncoder interface {
	encodePauseFrame(card *PauseCard, targetPTSMs int64) ([]byte, error)
}

// Session is a running transcode: encoded video/audio feeds, completion state, and pause control.
type Session struct {
	Video  <-chan Packet
	Audio  <-chan Packet
	Done   <-chan error
	pause *pauseState

	card    *PauseCard
	encoder pauseFrameEncoder

	frameMu    sync.Mutex
	frameEpoch uint64
	frameData  []byte
	frameBad   bool
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

// IsPaused reports the current pause state without blocking.
func (s *Session) IsPaused() bool {

	if s.pause == nil {
		return false
	}

	paused, _ := s.pause.snapshot()

	return paused

}

// PauseFrame returns the encoded pause-screen IDR for the current pause, rendering it
// once per pause over the frozen frame nearest targetPTSMs (the last video PTS sent;
// negative freezes the newest frame). Returns false when no card is configured,
// playback is not paused, or rendering failed.
func (s *Session) PauseFrame(targetPTSMs int64) ([]byte, bool) {

	if s.pause == nil || s.card == nil || s.encoder == nil {
		return nil, false
	}

	paused, epoch := s.pause.snapshot()

	if !paused {
		return nil, false
	}

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

// snapshot returns the pause flag and the current epoch (epoch increments on resume,
// so it uniquely identifies one pause while paused).
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

// TrimNativeHeap encourages libc/ffmpeg to return free pages to the OS after a transcode session.
func TrimNativeHeap() {
	trimNativeHeap()
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
		log.Printf("[transcode] libav started %q (direct input) at %s", label, request.Start)
	} else {
		log.Printf("[transcode] libav started %q at %s", label, request.Start)
	}

	return startNative(request)

}

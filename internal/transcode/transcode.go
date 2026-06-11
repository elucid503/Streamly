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
	Context      context.Context

	OnDuration func(durationMs int64) // Called once when the container duration is known.
}

// Session is a running transcode: encoded video/audio feeds, completion state, and pause control.
type Session struct {
	Video  <-chan Packet
	Audio  <-chan Packet
	Done   <-chan error
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

package transcode

import (
	"context"
	"sync"
	"time"
)

const liveBufferPollInterval = 25 * time.Millisecond

// LiveBuffer keeps a concrete encoded-ahead cushion for live HLS playback.
// Playback waits for the target depth before starting, holds on underrun, and
// slows consumption when the cushion grows past the target.
type LiveBuffer struct {
	target time.Duration
	minLag time.Duration

	mu              sync.Mutex
	headPTS         time.Duration
	pendingVideo    time.Duration
	pendingAudio    time.Duration
	hasPendingVideo bool
	hasPendingAudio bool
}

// NewLiveBuffer returns a live playback buffer with target and underrun thresholds.
func NewLiveBuffer(target, minLag time.Duration) *LiveBuffer {

	if minLag <= 0 || minLag > target {
		minLag = target / 3

		if minLag < time.Second {
			minLag = time.Second
		}

		if minLag > target {
			minLag = target
		}

	}

	return &LiveBuffer{
		target: target,
		minLag: minLag,
	}

}

// Target returns the startup and pacing cushion.
func (b *LiveBuffer) Target() time.Duration {

	return b.target

}

// MinLag returns the minimum encoded-ahead depth before playback advances.
func (b *LiveBuffer) MinLag() time.Duration {

	return b.minLag

}

// Observe records the PTS of a packet entering the playback queues.
func (b *LiveBuffer) Observe(pts time.Duration) {

	b.mu.Lock()
	defer b.mu.Unlock()

	if pts > b.headPTS {
		b.headPTS = pts
	}

}

// WaitBuffered blocks until every pending stream has enough encoded-ahead cushion.
func (b *LiveBuffer) WaitBuffered(ctx context.Context, pts time.Duration, required time.Duration, kind Kind) bool {

	if required <= 0 {
		return true
	}

	ticker := time.NewTicker(liveBufferPollInterval)
	defer ticker.Stop()

	for {

		b.mu.Lock()

		if kind == KindVideo {
			b.pendingVideo = pts
			b.hasPendingVideo = true
		} else {
			b.pendingAudio = pts
			b.hasPendingAudio = true
		}

		ready := b.readyLocked(required)
		b.mu.Unlock()

		if ready {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}

	}

}

// PacingDelay returns extra pacing time when encoded content is ahead of the target.
func (b *LiveBuffer) PacingDelay(pts time.Duration) time.Duration {

	b.mu.Lock()
	defer b.mu.Unlock()

	lag := b.lagLocked(pts)

	if lag <= b.target {
		return 0
	}

	return lag - b.target

}

func (b *LiveBuffer) readyLocked(required time.Duration) bool {

	checked := false
	ready := true

	if b.hasPendingVideo {
		checked = true

		if b.lagLocked(b.pendingVideo) < required {
			ready = false
		}

	}

	if b.hasPendingAudio {
		checked = true

		if b.lagLocked(b.pendingAudio) < required {
			ready = false
		}

	}

	return checked && ready

}

func (b *LiveBuffer) lagLocked(pts time.Duration) time.Duration {

	lag := b.headPTS - pts

	if lag < 0 {
		return 0
	}

	return lag

}
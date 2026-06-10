package transcode

import (
	"sync"
	"time"
)

// LiveJitter tracks how far ahead encoded packets are and throttles playback once the
// target cushion is full. Startup is unaffected: the first packet plays immediately and
// the demuxer fills the cushion while playback runs at 1x until lag reaches the target.
type LiveJitter struct {
	target  time.Duration
	mu      sync.Mutex
	headPTS time.Duration
}

// NewLiveJitter returns a jitter tracker for live HLS playback.
func NewLiveJitter(target time.Duration) *LiveJitter {

	return &LiveJitter{target: target}

}

// Observe records the PTS of a packet entering the playback queues.
func (j *LiveJitter) Observe(pts time.Duration) {

	j.mu.Lock()
	defer j.mu.Unlock()

	if pts > j.headPTS {
		j.headPTS = pts
	}

}

// Delay returns extra pacing time to keep buffered content at the target depth.
func (j *LiveJitter) Delay(pts time.Duration) time.Duration {

	j.mu.Lock()
	defer j.mu.Unlock()

	lag := j.headPTS - pts

	if lag <= j.target {
		return 0
	}

	return lag - j.target

}

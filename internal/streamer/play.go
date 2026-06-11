package streamer

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"streamly/internal/config"
	"streamly/internal/transcode"
)

const audioCorrectionThreshold = 350 * time.Millisecond

// Playback owns one Go Live stream and splices successive transcode sessions into it.
type Playback struct {
	streamer   *Streamer
	streamConn *StreamConnection
	sender     *mediaSender
}

// OpenPlayback starts the Go Live stream and waits until the media peer can send.
func OpenPlayback(ctx context.Context, s *Streamer) (*Playback, error) {

	if s.VoiceConnection() == nil {
		return nil, fmt.Errorf("not connected to a voice channel")
	}

	streamConn, err := s.CreateStream(ctx)

	if err != nil {
		return nil, err
	}

	readyCtx, readyCancel := context.WithTimeout(ctx, 15*time.Second)
	defer readyCancel()

	sender := &mediaSender{streamConn: streamConn}

	if _, err := sender.resolvePeer(readyCtx); err != nil {
		s.StopStream()

		return nil, fmt.Errorf("stream not ready: %w", err)
	}

	return &Playback{streamer: s, streamConn: streamConn, sender: sender}, nil

}

// Run plays one transcode session into the open stream and blocks until it ends.
func (p *Playback) Run(ctx context.Context, ts *transcode.Session) error {

	p.sender.beginSegment()

	return p.sender.run(ctx, ts)

}

// Close tears down the Go Live stream after the last session.
func (p *Playback) Close() {

	p.streamConn.setSpeaking(false)
	p.streamConn.setVideoAttributes(false, 0, 0, 0)
	p.streamer.StopStream()

}

// mediaSender ships the transcode's audio and video, pacing both against one shared clock.
type mediaSender struct {
	streamConn *StreamConnection
	activePeer *MediaPeer
	clock      mediaClock
	pauseMu    sync.Mutex
	pauseEpoch uint64

	rtpMu        sync.Mutex
	lastRTPAt    time.Time // When the RTP timeline last advanced (send, drop, or pause shift).
	gapPending   bool      // The next send must first cover the dead air since lastRTPAt.
	spliceActive bool      // Audio may lag video after a segment splice; resync instead of dropping.
}

// beginSegment resets pacing for a new transcode session; the RTP gap applies at the first send.
func (s *mediaSender) beginSegment() {

	s.pauseMu.Lock()
	s.pauseEpoch = 0
	s.pauseMu.Unlock()

	s.clock.reset()

	s.rtpMu.Lock()
	isSplice := !s.lastRTPAt.IsZero()
	s.gapPending = true
	s.spliceActive = false
	s.rtpMu.Unlock()

	if isSplice {
		if peer := s.activePeer; peer != nil && !peer.closed.Load() {
			s.streamConn.setSpeaking(true)
		}
	}

}

// markRTP records that the RTP timeline advanced just now.
func (s *mediaSender) markRTP() {

	s.rtpMu.Lock()
	s.lastRTPAt = time.Now()
	s.rtpMu.Unlock()

}

// segmentSpliceNeedsAudioResync reports whether a new session should resync audio after video anchors first.
func segmentSpliceNeedsAudioResync(pending bool, lastRTPAt time.Time) bool {
	return pending && !lastRTPAt.IsZero()
}

// applySegmentGap advances the RTP timeline across dead air between sessions, like pause/resume.
func (s *mediaSender) applySegmentGap(peer *MediaPeer) {

	s.rtpMu.Lock()

	pending := s.gapPending
	last := s.lastRTPAt
	s.gapPending = false
	s.lastRTPAt = time.Now()

	s.rtpMu.Unlock()

	if !segmentSpliceNeedsAudioResync(pending, last) {
		return
	}

	gap := time.Since(last)

	s.rtpMu.Lock()
	s.spliceActive = true
	s.rtpMu.Unlock()

	if gap <= 0 {
		return
	}

	peer.advanceAudio(gap)
	peer.advanceVideo(gap)

	log.Printf("[stream] segment spliced after %s gap", gap.Round(time.Millisecond))

}

func (s *mediaSender) resolvePeer(ctx context.Context) (*MediaPeer, error) {

	if s.activePeer != nil && s.activePeer.closed.Load() {
		s.activePeer = nil
	}

	peer := s.streamConn.peer()

	if peer == nil || peer.closed.Load() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()

		for {

			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("stream peer unavailable: %w", ctx.Err())
			case <-ticker.C:
				peer = s.streamConn.peer()

				if peer != nil && !peer.closed.Load() {
					goto found
				}

			}

		}

	}

found:

	if peer == s.activePeer {
		return peer, nil
	}

	if err := peer.waitSendReady(ctx); err != nil {
		return nil, err
	}

	s.activePeer = peer
	s.streamConn.setSpeaking(true)
	s.streamConn.setVideoAttributes(true, config.Stream.Width, config.Stream.Height, config.Stream.FrameRate)

	return peer, nil

}

// run drains both feeds concurrently so the encoder never blocks, while one clock keeps A/V in sync.
func (s *mediaSender) run(ctx context.Context, ts *transcode.Session) error {

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 2)

	go func() { errs <- s.pump(ctx, ts, ts.Video, transcode.KindVideo) }()
	go func() { errs <- s.pump(ctx, ts, ts.Audio, transcode.KindAudio) }()

	var first error

	for i := 0; i < 2; i++ {

		if err := <-errs; err != nil && first == nil {
			first = err
			cancel()
		}

	}

	return first

}

// pump sends one feed in order, pacing each packet against the shared clock and retrying on backpressure.
func (s *mediaSender) pump(ctx context.Context, ts *transcode.Session, packets <-chan transcode.Packet, kind transcode.Kind) error {

	for {

		if !ts.WaitIfPaused(ctx) {
			return ctx.Err()
		}

		s.applyPauseEvent(ts)

		select {
		case <-ctx.Done():
			return ctx.Err()

		case packet, ok := <-packets:

			if !ok {
				return nil
			}

			if !ts.WaitIfPaused(ctx) {
				return ctx.Err()
			}

			s.applyPauseEvent(ts)

			if !s.clock.wait(ctx, packet.PTS, 0) {
				return ctx.Err()
			}

			duration := packet.Duration

			if kind == transcode.KindVideo {
				duration = frametime(kind)
			} else if duration <= 0 {
				duration = frametime(kind)
			}

			peer, err := s.resolvePeer(ctx)

			if err != nil {
				return err
			}

			s.applySegmentGap(peer)

			if kind == transcode.KindVideo {

				peer.sendVideo(packet.Data, duration)
				s.markRTP()

			} else {
				s.rtpMu.Lock()
				resync := s.spliceActive
				if resync {
					s.spliceActive = false
				}
				s.rtpMu.Unlock()

				if resync {
					if late := s.clock.lateness(packet.PTS); late > 0 {
						s.clock.shift(late)
					}

					peer.sendAudio(packet.Data, duration)
					s.markRTP()

					continue
				}

				if late := s.clock.lateness(packet.PTS); late > audioCorrectionThreshold {
					peer.advanceAudio(duration)
					s.markRTP()

					continue
				}

				peer.sendAudio(packet.Data, duration)
				s.markRTP()
			}

		}

	}

}

func (s *mediaSender) applyPauseEvent(ts *transcode.Session) {

	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()

	duration, epoch := ts.PauseEvent(s.pauseEpoch)

	if epoch == s.pauseEpoch {
		return
	}

	s.pauseEpoch = epoch

	if duration <= 0 {
		return
	}

	if peer := s.activePeer; peer != nil {
		s.clock.shift(duration)
		peer.advanceAudio(duration)
		peer.advanceVideo(duration)
		s.markRTP()
	}

}

// mediaClock is the single playback clock both feeds pace against, keeping audio and video in sync.
type mediaClock struct {
	mu        sync.Mutex
	wallStart time.Time
	ptsStart  time.Duration
	anchored  bool
}

// wait blocks until the shared clock reaches pts; extraDelay slows live jitter when the cushion is full.
func (c *mediaClock) wait(ctx context.Context, pts time.Duration, extraDelay time.Duration) bool {

	c.mu.Lock()

	if !c.anchored {
		c.wallStart = time.Now()
		c.ptsStart = pts
		c.anchored = true
		c.mu.Unlock()

		return true
	}

	sleep := (pts - c.ptsStart) - time.Since(c.wallStart)
	c.mu.Unlock()

	if extraDelay > 0 {
		sleep += extraDelay
	}

	if sleep <= 0 {
		return true
	}

	timer := time.NewTimer(sleep)

	select {
	case <-ctx.Done():
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		return false
	case <-timer.C:
		return true
	}

}

func (c *mediaClock) lateness(pts time.Duration) time.Duration {

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.anchored {
		return 0
	}

	expected := pts - c.ptsStart
	actual := time.Since(c.wallStart)

	if actual <= expected {
		return 0
	}

	return actual - expected

}

func (c *mediaClock) shift(duration time.Duration) {

	if duration <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.anchored {
		c.wallStart = c.wallStart.Add(duration)
	}

}

// reset un-anchors the clock so the next session's first packet re-anchors it.
func (c *mediaClock) reset() {

	c.mu.Lock()
	defer c.mu.Unlock()

	c.anchored = false

}

func frametime(kind transcode.Kind) time.Duration {

	if kind == transcode.KindVideo {

		fps := config.Stream.FrameRate

		if fps <= 0 {
			fps = 30
		}

		return time.Second / time.Duration(fps)

	}

	return 20 * time.Millisecond

}

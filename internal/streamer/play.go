package streamer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"streamly/internal/config"
	"streamly/internal/transcode"
)

const audioCorrectionThreshold = 350 * time.Millisecond

// Play streams a running transcode's encoded packets into Discord Go Live.
func Play(ctx context.Context, s *Streamer, ts *transcode.Session) error {

	if s.VoiceConnection() == nil {
		return fmt.Errorf("not connected to a voice channel")
	}

	streamConn, err := s.CreateStream(ctx)

	if err != nil {
		return err
	}

	defer func() {

		streamConn.setSpeaking(false)
		streamConn.setVideoAttributes(false, 0, 0, 0)
		s.StopStream()

	}()

	readyCtx, readyCancel := context.WithTimeout(ctx, 15*time.Second)
	defer readyCancel()

	sender := &mediaSender{streamConn: streamConn}

	if _, err := sender.resolvePeer(readyCtx); err != nil {
		return fmt.Errorf("stream not ready: %w", err)
	}

	return sender.run(ctx, ts)

}

// mediaSender ships the transcode's audio and video, pacing both against one shared clock.
type mediaSender struct {
	streamConn *StreamConnection
	activePeer *MediaPeer
	clock      mediaClock
	pauseMu    sync.Mutex
	pauseEpoch uint64
}

func (s *mediaSender) resolvePeer(ctx context.Context) (*MediaPeer, error) {

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

			if !s.clock.wait(ctx, packet.PTS) {
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

			if kind == transcode.KindVideo {

				peer.sendVideo(packet.Data, duration)

			} else {
				if late := s.clock.lateness(packet.PTS); late > audioCorrectionThreshold {
					peer.advanceAudio(duration)

					continue
				}

				peer.sendAudio(packet.Data, duration)
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
	}

}

// mediaClock is the single playback clock both feeds pace against, keeping audio and video in sync.
type mediaClock struct {
	mu        sync.Mutex
	wallStart time.Time
	ptsStart  time.Duration
	anchored  bool
}

// wait blocks until the shared clock reaches pts. It anchors once on the first packet seen across both
// feeds; a feed that starts late (x264 buffers frames before audio) simply fast-forwards its backlog to
// catch up rather than re-anchoring, which would corrupt the other feed's pacing.
func (c *mediaClock) wait(ctx context.Context, pts time.Duration) bool {

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

	if sleep <= 0 {
		return true
	}

	select {
	case <-ctx.Done():
		return false
	case <-time.After(sleep):
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

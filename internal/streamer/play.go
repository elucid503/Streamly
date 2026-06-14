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

const bufferHoldThreshold = 350 * time.Millisecond

// pauseFrameInterval keeps clients out of buffering without exceeding the video bitrate cap.
const pauseFrameInterval = 500 * time.Millisecond

type Playback struct {

	streamer *Streamer
	streamConn *StreamConnection
	sender *mediaSender

}

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

func (p *Playback) Run(ctx context.Context, ts *transcode.Session) error {

	p.sender.beginSegment()

	return p.sender.run(ctx, ts)

}

func (p *Playback) Close() {

	p.streamConn.setSpeaking(false)
	p.streamConn.setVideoAttributes(false, 0, 0, 0)
	p.streamer.StopStream()

}

type mediaSender struct {

	streamConn *StreamConnection
	activePeer *MediaPeer
	clock mediaClock

	pauseMu sync.Mutex
	pauseEpoch uint64
	pauseSent time.Duration
	loadingSent time.Duration
	dropToIDR bool

	lastVideoPTSMs int64

	rtpMu sync.Mutex
	lastRTPAt time.Time
	gapPending bool

}

func (s *mediaSender) beginSegment() {

	s.pauseMu.Lock()
	s.pauseEpoch = 0
	s.dropToIDR = false
	s.pauseMu.Unlock()

	s.lastVideoPTSMs = -1

	s.clock.reset()

	s.rtpMu.Lock()
	isSplice := !s.lastRTPAt.IsZero()
	s.gapPending = true
	s.rtpMu.Unlock()

	if isSplice {

		if peer := s.activePeer; peer != nil && !peer.closed.Load() {

			s.streamConn.setSpeaking(true)

		}

	}

}

func (s *mediaSender) markRTP() {

	s.rtpMu.Lock()
	s.lastRTPAt = time.Now()
	s.rtpMu.Unlock()

}

func segmentSpliceNeedsAudioResync(pending bool, lastRTPAt time.Time) bool {

	return pending && !lastRTPAt.IsZero()

}

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

// holdWhilePaused re-sends the pause-card IDR so clients never enter a loading state.
func (s *mediaSender) holdWhilePaused(ctx context.Context, ts *transcode.Session, kind transcode.Kind) bool {

	if kind != transcode.KindVideo {

		return ts.WaitIfPaused(ctx)

	}

	for ts.IsPaused() {

		s.sendPauseFrame(ctx, ts)

		select {

		case <-ctx.Done():
			return false
		case <-time.After(pauseFrameInterval):

		}

	}

	return ctx.Err() == nil

}

func (s *mediaSender) sendPauseFrame(ctx context.Context, ts *transcode.Session) {

	frame, ok := ts.PauseFrame(s.lastVideoPTSMs)

	if !ok {

		return

	}

	peer, err := s.resolvePeer(ctx)

	if err != nil {

		return

	}

	s.applySegmentGap(peer)

	peer.sendVideo(frame, pauseFrameInterval)
	s.markRTP()

	s.pauseMu.Lock()
	s.pauseSent += pauseFrameInterval
	s.pauseMu.Unlock()

}

func (s *mediaSender) sendLoadingFrame(ctx context.Context, ts *transcode.Session) {

	frame, ok := ts.LoadingFrame(s.lastVideoPTSMs)

	if !ok {

		return

	}

	peer, err := s.resolvePeer(ctx)

	if err != nil {

		return

	}

	s.applySegmentGap(peer)

	peer.sendVideo(frame, pauseFrameInterval)
	s.markRTP()

	s.pauseMu.Lock()
	s.loadingSent += pauseFrameInterval
	s.dropToIDR = true
	s.pauseMu.Unlock()

}

func (s *mediaSender) applyLoadingHold(peer *MediaPeer) {

	s.pauseMu.Lock()
	duration := s.loadingSent
	s.loadingSent = 0
	s.pauseMu.Unlock()

	if duration <= 0 {

		return

	}

	s.clock.shift(duration)
	peer.advanceAudio(duration)
	s.markRTP()

}

// consumeVideoDrop drops P-frames after a pause card until the next in-stream IDR re-anchors refs.
func (s *mediaSender) consumeVideoDrop(frame []byte) bool {

	s.pauseMu.Lock()
	defer s.pauseMu.Unlock()

	if !s.dropToIDR {

		return false

	}

	if h264ContainsIDR(frame) {

		s.dropToIDR = false
		return false

	}

	return true

}

func (s *mediaSender) nextPacket(ctx context.Context, ts *transcode.Session, packets <-chan transcode.Packet, kind transcode.Kind) (transcode.Packet, bool, error) {

	if kind != transcode.KindVideo || s.lastVideoPTSMs < 0 {

		select {

		case <-ctx.Done():
			return transcode.Packet{}, false, ctx.Err()
		case packet, ok := <-packets:
			return packet, ok, nil

		}

	}

	timer := time.NewTimer(bufferHoldThreshold)
	defer timer.Stop()

	for {

		select {

		case <-ctx.Done():
			return transcode.Packet{}, false, ctx.Err()
		case packet, ok := <-packets:
			return packet, ok, nil
		case <-timer.C:
			s.sendLoadingFrame(ctx, ts)
			timer.Reset(pauseFrameInterval)

		}

	}

}

func (s *mediaSender) pump(ctx context.Context, ts *transcode.Session, packets <-chan transcode.Packet, kind transcode.Kind) error {

	for {

		if !s.holdWhilePaused(ctx, ts, kind) {

			return ctx.Err()

		}

		s.applyPauseEvent(ts)

		packet, ok, err := s.nextPacket(ctx, ts, packets, kind)

		if err != nil {

			return err

		}

		if !ok {

			return nil

		}

		if !s.holdWhilePaused(ctx, ts, kind) {

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
		s.applyLoadingHold(peer)

		if kind == transcode.KindVideo {

			if s.consumeVideoDrop(packet.Data) {

				peer.advanceVideo(duration)
				s.markRTP()

				continue

			}

			peer.sendVideo(packet.Data, duration)
			s.lastVideoPTSMs = packet.PTS.Milliseconds()
			s.markRTP()

		} else {

			if late := s.clock.lateness(packet.PTS); late > bufferHoldThreshold {

				s.clock.shift(late)

			}

			peer.sendAudio(packet.Data, duration)
			s.markRTP()

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

	// pauseSent already advanced video RTP; fold any overshoot into audio so both tracks stay aligned.
	sent := s.pauseSent
	s.pauseSent = 0

	videoGap := duration - sent
	audioGap := duration

	if videoGap < 0 {

		audioGap -= videoGap
		videoGap = 0

	}

	if sent > 0 {

		s.dropToIDR = true

	}

	if peer := s.activePeer; peer != nil {

		s.clock.shift(duration)
		peer.advanceAudio(audioGap)

		if videoGap > 0 {

			peer.advanceVideo(videoGap)

		}

		s.markRTP()

	}

}

type mediaClock struct {

	mu sync.Mutex
	wallStart time.Time
	ptsStart time.Duration
	anchored bool

}

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

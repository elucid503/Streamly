package pool

import (
	"context"
	"time"

	"streamly/internal/transcode"
)

const (
	IntroSkipCTAText     = "Use /skip-intro to jump ahead"
	liveCTADurationMs    = 8000
	playbackPollInterval = 2 * time.Second
	creditsCTAPrefix     = "Check #"
)

// SegmentCTA is a transient callout shown at the start of one transcode segment.
type SegmentCTA struct {
	Text       string
	DurationMs int64
}

// TimedCTA is a callout anchored to absolute media timestamps.
type TimedCTA struct {
	Text    string
	StartMs int64
	EndMs   int64
}

// SetCTAFont configures the drawtext font for on-stream callouts.
func (session *Session) SetCTAFont(path string) {
	session.ctaFontPath = path
}

// SetTimedCTA registers a position-anchored callout for upcoming segments.
func (session *Session) SetTimedCTA(cta TimedCTA) {
	session.timedCTAs = append(session.timedCTAs, cta)
}

// HasIntroCTA reports whether the skip-intro overlay is armed for this session.
func (session *Session) HasIntroCTA() bool {

	for _, cta := range session.timedCTAs {
		if cta.Text == IntroSkipCTAText {
			return true
		}
	}

	return false

}

// HasCreditsCTA reports whether the credits auto-next overlay is armed for this session.
func (session *Session) HasCreditsCTA() bool {

	for _, cta := range session.timedCTAs {
		if len(cta.Text) >= len(creditsCTAPrefix) && cta.Text[:len(creditsCTAPrefix)] == creditsCTAPrefix {
			return true
		}
	}

	return false

}

// SetCreditsTrigger arms auto-next when playback reaches the credits window.
func (session *Session) SetCreditsTrigger(positionMs int64) {
	session.creditsTriggerMs = positionMs
}

func (session *Session) buildCTAWindows(offset time.Duration) []transcode.CTAWindow {

	offsetMs := offset.Milliseconds()

	windows := make([]transcode.CTAWindow, 0, len(session.pendingSegmentCTAs)+len(session.timedCTAs))

	for _, cta := range session.timedCTAs {

		if cta.Text == "" || cta.EndMs <= offsetMs {
			continue
		}

		startMs := cta.StartMs - offsetMs

		if startMs < 0 {
			startMs = 0
		}

		endMs := cta.EndMs - offsetMs

		windows = append(windows, transcode.CTAWindow{
			Text:    cta.Text,
			StartMs: startMs,
			EndMs:   endMs,
		})

	}

	for _, cta := range session.pendingSegmentCTAs {
		if cta.Text == "" {
			continue
		}

		startMs := offset.Milliseconds()
		endMs := startMs + cta.DurationMs

		if cta.DurationMs <= 0 {
			endMs = startMs + liveCTADurationMs
		}

		windows = append(windows, transcode.CTAWindow{
			Text:    cta.Text,
			StartMs: startMs,
			EndMs:   endMs,
		})

	}

	session.pendingSegmentCTAs = nil

	return windows

}

func (session *Session) enrichTranscodeRequest(treq *transcode.Request, offset time.Duration) {

	if windows := session.buildCTAWindows(offset); len(windows) > 0 {
		treq.CTAs = windows
	}

	if treq.CTAFontPath == "" && session.ctaFontPath != "" {
		treq.CTAFontPath = session.ctaFontPath
	}

}

func (p *Pool) monitorPlayback(ctx context.Context, session *Session, request Request) {

	if request.OnNearEnd == nil || session.Metadata == nil || session.Metadata.Episode == nil {
		return
	}

	ticker := time.NewTicker(playbackPollInterval)
	defer ticker.Stop()

	for {

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if !session.Busy || session.nearEndTriggered {
			return
		}

		stats := p.Stats(session)

		if stats.DurationMs == nil || *stats.DurationMs <= 0 {
			continue
		}

		if session.creditsTriggerMs > 0 && stats.PositionMs >= session.creditsTriggerMs {
			session.nearEndTriggered = true
			if request.OnNearEnd != nil {
				request.OnNearEnd()
			}
			return
		}

	}

}
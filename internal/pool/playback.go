package pool

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"streamly/internal/transcode"
)

const (

	IntroSkipCTAText = "Use /skip-intro to jump ahead"
	PauseCTAText = "Use /resume to resume playback."
	LoadingCTAText = "Loading live stream..."
	creditsCTAPrefix = "Check #"

	liveCTADurationMs = 8000
	playbackPollInterval = 2 * time.Second

	pauseBodyLineWidth = 64
	pauseBodyMaxLines = 3

	// liveReconnectCTAText is shown after the first failed attempt.
	liveReconnectCTAText = "Reconnecting live stream..."
	// liveFallbackCTAText is shown after switching to the backup provider.
	liveFallbackCTAText = "Trying backup source..."

)

type SegmentCTA struct {

	Text string
	DurationMs int64
}

type TimedCTA struct {

	Text string
	StartMs int64
	EndMs int64
}

func (session *Session) SetCTAFont(path string) {

	session.ctaFontPath = path

}

func (session *Session) SetTimedCTA(cta TimedCTA) {

	session.timedCTAs = append(session.timedCTAs, cta)

}

func (session *Session) HasIntroCTA() bool {

	for _, cta := range session.timedCTAs {

		if cta.Text == IntroSkipCTAText {

			return true

		}

	}

	return false

}

func (session *Session) HasCreditsCTA() bool {

	for _, cta := range session.timedCTAs {

		if len(cta.Text) >= len(creditsCTAPrefix) && cta.Text[:len(creditsCTAPrefix)] == creditsCTAPrefix {

			return true

		}

	}

	return false

}

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

			Text: cta.Text,

			StartMs: startMs,
			EndMs: endMs,

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

			Text: cta.Text,

			StartMs: startMs,
			EndMs: endMs,

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

	if treq.Live {

		treq.PauseCard = session.buildLoadingCard(treq.Caption)

	} else {

		treq.PauseCard = session.buildPauseCard(treq.Caption)

	}

}

func (session *Session) buildLoadingCard(caption string) *transcode.PauseCard {

	card := session.buildPauseCard(caption)

	switch {

	case session.liveAttempt >= liveProviderFallbackAfter:
		card.CTA = fmt.Sprintf("%s (attempt %d)", liveFallbackCTAText, session.liveAttempt+1)

	case session.liveAttempt > 0:
		card.CTA = fmt.Sprintf("%s (attempt %d)", liveReconnectCTAText, session.liveAttempt+1)

	default:
		card.CTA = LoadingCTAText

	}

	if card.Title == "Paused" {

		card.Title = "Loading"

	}

	return card

}

func (session *Session) buildPauseCard(caption string) *transcode.PauseCard {

	card := &transcode.PauseCard{

		Title: caption,
		CTA: PauseCTAText,

		FontPath: session.ctaFontPath,

	}

	if meta := session.Metadata; meta != nil {

		if meta.Details.Title != "" {

			card.Title = meta.Details.Title

		} else if meta.VideoName != "" {

			card.Title = meta.VideoName

		}

		if meta.Episode != nil {

			card.Subtitle = fmt.Sprintf("S%dE%d", meta.Episode.Season, meta.Episode.Episode)

			name := meta.Episode.Title

			if name == "" {

				key := fmt.Sprintf("%d:%d", meta.Episode.Season, meta.Episode.Episode)
				name = meta.Details.EpisodeTitles[key]

			}

			if name != "" {

				card.Subtitle += " — " + name

			}

		}

		card.BodyLines = wrapPauseBody(meta.Details.Description, pauseBodyLineWidth, pauseBodyMaxLines)

	}

	if card.Title == "" {

		card.Title = "Paused"
	}

	return card

}

func wrapPauseBody(text string, width, maxLines int) []string {

	words := strings.Fields(text)

	if len(words) == 0 {

		return nil
	}

	lines := []string{""}

	for _, word := range words {

		if utf8.RuneCountInString(word) > width {

			word = truncateRunes(word, width)

		}

		line := lines[len(lines)-1]

		switch {

			case line == "":

				lines[len(lines)-1] = word

			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(word) <= width:

				lines[len(lines)-1] = line + " " + word

			default:

				lines = append(lines, word)

		}

	}

	if len(lines) > maxLines {

		lines = lines[:maxLines]
		lines[maxLines-1] = truncateRunes(lines[maxLines-1], width-3) + "..."

	}

	return lines

}

func truncateRunes(text string, max int) string {

	if max <= 0 {

		return ""
	}

	runes := []rune(text)

	if len(runes) <= max {

		return text
	}

	return string(runes[:max])

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

			case <-ticker.C: // check playback position every interval

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

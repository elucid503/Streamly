package pool

import (
	"fmt"

	"streamly/internal/introdb"
)

// CreditsCTAText formats the on-stream auto-next callout for the text channel.
func CreditsCTAText(channelName string) string {

	if channelName == "" {
		return "Check the stream channel to continue watching"
	}

	return fmt.Sprintf("Check #%s to continue watching", channelName)

}

func (session *Session) armCreditsTrigger(durationMs int64) {

	if session.creditsTriggerMs > 0 || session.Metadata == nil || session.Metadata.AutoNext == nil {
		return
	}

	if session.Metadata.IntroRecord == nil {
		return
	}

	if start, ok := introdb.CreditsStart(session.Metadata.IntroRecord, durationMs); ok {
		session.creditsTriggerMs = start.Milliseconds()
		session.armCreditsCTA(durationMs)
	}

}

func (session *Session) armCreditsCTA(durationMs int64) {

	if session.HasCreditsCTA() || session.creditsTriggerMs <= 0 || durationMs <= 0 {
		return
	}

	channelName := ""

	if session.Metadata != nil {
		channelName = session.Metadata.TextChannelName
	}

	session.SetTimedCTA(TimedCTA{
		Text:    CreditsCTAText(channelName),
		StartMs: session.creditsTriggerMs,
		EndMs:   durationMs,
	})

}
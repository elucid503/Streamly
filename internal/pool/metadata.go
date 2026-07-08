package pool

import (
	"streamly/internal/introdb"
	"streamly/internal/media"
	"streamly/internal/tvapi"
)

type EpisodeRef struct {

	Season int
	Episode int

	Title string

}

type AutoNextContext struct {

	ShowID int
	UserID string

	ShareKey string

	Season int
	Episode int

	HistoryValue string

	ChannelID string
	VoiceChannelID string

}

type StreamMetadata struct {

	FID int
	UserID string
	ChannelID string

	ShareKey string

	Target int
	Label string

	Live bool
	VideoName string

	Details media.TitleDetails
	Episode *EpisodeRef
	TVChannel *tvapi.Channel

	AutoNext *AutoNextContext

	CaptionsPreferred bool

	TextChannelID string
	TextChannelName string

	IntroRecord *introdb.MediaRecord

}

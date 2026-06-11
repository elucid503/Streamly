package pool

import (
	"streamly/internal/introdb"
	"streamly/internal/media"
	"streamly/internal/tvapi"
)

// EpisodeRef identifies a TV episode within a season.
type EpisodeRef struct {
	Season  int
	Episode int
	Title   string
}

// AutoNextContext holds everything needed to queue the next TV episode.
type AutoNextContext struct {
	ShowID          int
	ShareKey        string
	Season          int
	Episode         int
	HistoryValue    string
	ChannelID       string // Text channel where /stream was invoked.
	VoiceChannelID  string
	UserID          string
}

// StreamMetadata is per-session VOD/live context used by bot handlers and playback hooks.
type StreamMetadata struct {
	ShareKey  string
	FID       int
	VideoName string
	Target    int
	Label     string
	Live      bool
	DaddyID   string
	Details   media.TitleDetails
	Episode   *EpisodeRef
	TVChannel *tvapi.Channel
	AutoNext  *AutoNextContext

	UserID            string
	CaptionsPreferred bool
	TextChannelID     string
	TextChannelName   string
	IntroRecord       *introdb.MediaRecord
}
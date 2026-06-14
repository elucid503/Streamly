package captions

import "errors"

var (

	ErrNoFont = errors.New("captions: assets/font.ttf not found")
	ErrNoSubtitle = errors.New("captions: no English subtitles found")
	ErrUnconfigured = errors.New("captions: SUBDL_API_KEY not configured")
	ErrUnauthorized = errors.New("captions: subtitle provider unauthorized")
	ErrRateLimited = errors.New("captions: subtitle provider rate limited")
	ErrUnseekable = errors.New("captions: source cannot be restarted for burn-in")
	ErrLiveUnsupported = errors.New("captions: live streams do not support captions")
	ErrNoMetadata = errors.New("captions: stream metadata unavailable")

)

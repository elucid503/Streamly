package febapi

import "streamly/internal/textutil"

// DecodeText normalizes user-facing strings from Showbox API payloads.
func DecodeText(value string) string {
	return textutil.DecodeHTML(value)
}
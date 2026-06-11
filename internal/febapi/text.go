package febapi

import (
	"html"
	"strings"
)

// DecodeText normalizes user-facing strings from Showbox API payloads.
func DecodeText(value string) string {
	return strings.TrimSpace(html.UnescapeString(value))
}
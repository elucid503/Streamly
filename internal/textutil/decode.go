package textutil

import (
	"html"
	"strings"
)

// DecodeHTML normalizes user-facing strings that may contain HTML entities.
func DecodeHTML(value string) string {
	return strings.TrimSpace(html.UnescapeString(value))
}
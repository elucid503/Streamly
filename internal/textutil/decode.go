package textutil

import (
	"html"
	"strings"
)

func DecodeHTML(value string) string {

	return strings.TrimSpace(html.UnescapeString(value))

}

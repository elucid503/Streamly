package source

import "strings"

// IsHlsURL reports whether a URL points at an HLS playlist rather than a progressive file.
func IsHlsURL(raw string) bool {

	path := strings.ToLower(strings.SplitN(raw, "?", 2)[0])

	return strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".m3u")

}

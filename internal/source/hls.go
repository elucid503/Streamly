package source

import "strings"

// IsHlsURL reports whether a URL points at an HLS playlist rather than a progressive file.
func IsHlsURL(raw string) bool {

	lower := strings.ToLower(raw)
	path := strings.SplitN(lower, "?", 2)[0]

	if strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".m3u") {
		return true
	}

	// Proxied live playlists often omit a .m3u8 suffix in the path.
	if strings.Contains(path, "/papi/tv/playlist/") || strings.Contains(path, "/api/proxy/playlist") {
		return true
	}

	return false

}

package tvapi

import (
	"fmt"
	"strings"
)

// ResolveStream returns a direct CDN HLS playlist for a catalog channel daddyId.
// Streams are resolved from the DLHD embed page so libav receives the embed
// Referer required by obfuscated segment CDNs.
func (c *TVClient) ResolveStream(daddyID string) (ResolvedStream, error) {

	daddyID = strings.TrimSpace(daddyID)

	if daddyID == "" {
		return ResolvedStream{}, fmt.Errorf("daddyId is required")
	}

	stream, err := c.resolveDLHD(daddyID)

	if err != nil {
		return ResolvedStream{}, err
	}

	if !isHLSPlaylistURL(stream.URL) {
		return ResolvedStream{}, fmt.Errorf("not an hls playlist: %s", stream.URL)
	}

	return stream, nil

}

func isHLSPlaylistURL(raw string) bool {

	lower := strings.ToLower(strings.TrimSpace(raw))
	path := strings.SplitN(lower, "?", 2)[0]

	if strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".m3u") {
		return true
	}

	return strings.Contains(path, "/papi/tv/playlist/")

}
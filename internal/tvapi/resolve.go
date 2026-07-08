package tvapi

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var (
	playerB64VarRE    = regexp.MustCompile(`var\s+([A-Za-z_$][\w$]*)\s*=\s*'([^']*)'`)
	playerB64ConcatRE = regexp.MustCompile(`=\s*((?:[A-Za-z_$][\w$]*\([A-Za-z_$][\w$]*\)\+?)+)\s*;`)
	playerB64CallRE   = regexp.MustCompile(`[A-Za-z_$][\w$]*\(([A-Za-z_$][\w$]*)\)`)
)

func (c *TVClient) ResolveStream(channelID string) (ResolvedStream, error) {

	channelID = strings.TrimSpace(channelID)

	if channelID == "" {

		return ResolvedStream{}, fmt.Errorf("channel id is required")

	}

	playerURL, err := c.channelPlayerURL(channelID)

	if err != nil {

		return ResolvedStream{}, err

	}

	stream, err := c.resolveCdnlive(playerURL)

	if err != nil {

		return ResolvedStream{}, err

	}

	if !isHLSPlaylistURL(stream.URL) {

		return ResolvedStream{}, fmt.Errorf("not an hls playlist: %s", stream.URL)

	}

	// cdnlivetv playlists are CORS-open; no referer/proxy session required.
	stream.Referer = ""

	return stream, nil

}

func (c *TVClient) channelPlayerURL(channelID string) (string, error) {

	catalog := c.cachedCatalog()

	if catalog == nil {

		return "", fmt.Errorf("catalog unavailable")

	}

	channel, ok := catalog.FindByID(channelID)

	if !ok {

		return "", fmt.Errorf("channel not found: %s", channelID)

	}

	playerURL := strings.TrimSpace(channel.ChannelURL)

	if playerURL == "" {

		return "", fmt.Errorf("channel missing player url: %s", channelID)

	}

	return playerURL, nil

}

func (c *TVClient) resolveCdnlive(playerURL string) (ResolvedStream, error) {

	response, err := c.get(playerURL, c.baseURL+"/")

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("fetch cdnlive player: %w", err)

	}

	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("read cdnlive player: %w", err)

	}

	if response.StatusCode != http.StatusOK {

		return ResolvedStream{}, fmt.Errorf("fetch cdnlive player: status %d", response.StatusCode)

	}

	playlistURL, ok := extractCdnlivePlaylist(string(body))

	if !ok {

		return ResolvedStream{}, fmt.Errorf("cdnlive player missing playlist source")

	}

	return ResolvedStream{URL: playlistURL}, nil

}

// extractCdnlivePlaylist reconstructs the HLS URL from base64 var fragments in the
// player HTML (no JS execution). Fragments are URL-safe base64 pieces concatenated
// by a decoder call chain on the page.
func extractCdnlivePlaylist(page string) (string, bool) {

	vars := map[string]string{}

	for _, match := range playerB64VarRE.FindAllStringSubmatch(page, -1) {

		vars[match[1]] = match[2]

	}

	concat := playerB64ConcatRE.FindStringSubmatch(page)

	if len(concat) < 2 {

		return "", false

	}

	calls := playerB64CallRE.FindAllStringSubmatch(concat[1], -1)

	if len(calls) == 0 {

		return "", false

	}

	var b strings.Builder

	for _, call := range calls {

		raw, ok := vars[call[1]]

		if !ok {

			return "", false

		}

		decoded, err := decodePlayerB64(raw)

		if err != nil {

			return "", false

		}

		b.WriteString(decoded)

	}

	playlistURL := strings.TrimSpace(b.String())

	if playlistURL == "" || (!strings.HasPrefix(playlistURL, "http://") && !strings.HasPrefix(playlistURL, "https://")) {

		return "", false

	}

	return playlistURL, true

}

func decodePlayerB64(raw string) (string, error) {

	raw = strings.TrimSpace(raw)
	raw = strings.ReplaceAll(raw, "-", "+")
	raw = strings.ReplaceAll(raw, "_", "/")

	switch len(raw) % 4 {

	case 2:

		raw += "=="

	case 3:

		raw += "="

	}

	decoded, err := base64.StdEncoding.DecodeString(raw)

	if err != nil {

		return "", err

	}

	return string(decoded), nil

}

func isHLSPlaylistURL(raw string) bool {

	lower := strings.ToLower(strings.TrimSpace(raw))

	path := strings.SplitN(lower, "?", 2)[0]

	return strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".m3u")

}

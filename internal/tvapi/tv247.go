package tvapi

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const tv247FallbackBaseURL = "https://chat.cfbu247.sbs/api/resolve-dlstream/"

// resolveTV247 resolves a channel via the cfbu247 proxy API, returning a proxied HLS playlist.
func (c *TVClient) resolveTV247(daddyID string) (ResolvedStream, error) {

	resolveURL := strings.TrimRight(tv247FallbackBaseURL, "/") + "/" + url.PathEscape(daddyID)
	referer := "https://cfbu247.sbs/"

	response, err := c.get(resolveURL, referer)

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("tv247 resolve: %w", err)

	}

	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("tv247 read: %w", err)

	}

	if response.StatusCode != http.StatusOK {

		return ResolvedStream{}, fmt.Errorf("tv247 resolve: status %d", response.StatusCode)

	}

	streamURL, err := parseResolveResponse(body)

	if err != nil {

		return ResolvedStream{}, err

	}

	if streamURL == "" {

		return ResolvedStream{}, fmt.Errorf("tv247 resolve: empty stream")

	}

	if !strings.HasPrefix(streamURL, "http://") && !strings.HasPrefix(streamURL, "https://") {

		return ResolvedStream{}, fmt.Errorf("tv247 resolve: relative URL not supported: %s", streamURL)

	}

	if !isHLSPlaylistURL(streamURL) {

		return ResolvedStream{}, fmt.Errorf("tv247 resolve: not an HLS playlist: %s", streamURL)

	}

	return ResolvedStream{

		URL: streamURL,
		Referer: referer,

	}, nil

}

// ResolveStreamFallback resolves a channel via the TV247/cfbu247 fallback API.
func (c *TVClient) ResolveStreamFallback(daddyID string) (ResolvedStream, error) {

	daddyID = strings.TrimSpace(daddyID)

	if daddyID == "" {

		return ResolvedStream{}, fmt.Errorf("daddyId is required")

	}

	return c.resolveTV247(daddyID)

}

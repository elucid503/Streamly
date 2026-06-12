package tvapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

type streamResolver func(string) (ResolvedStream, error)

// ResolveStream returns the first working HLS playlist for a catalog channel daddyId.
func (c *TVClient) ResolveStream(daddyID string) (ResolvedStream, error) {

	daddyID = strings.TrimSpace(daddyID)

	if daddyID == "" {
		return ResolvedStream{}, fmt.Errorf("daddyId is required")
	}

	var errs []error

	for _, resolve := range c.streamResolvers() {

		stream, err := resolve(daddyID)

		if err != nil {
			errs = append(errs, err)
			continue
		}

		if !isHLSPlaylistURL(stream.URL) {
			errs = append(errs, fmt.Errorf("not an hls playlist: %s", stream.URL))
			continue
		}

		return stream, nil

	}

	if len(errs) == 0 {
		return ResolvedStream{}, fmt.Errorf("no stream resolvers configured")
	}

	return ResolvedStream{}, errors.Join(errs...)

}

func (c *TVClient) streamResolvers() []streamResolver {

	resolvers := []streamResolver{
		c.resolveLegacy,
		c.resolveDLHD,
	}

	if api := strings.TrimSpace(os.Getenv("TV_STREAM_API")); api != "" {
		resolvers = append([]streamResolver{c.resolveStreamAPI}, resolvers...)
	}

	return resolvers

}

// resolveLegacy asks the catalog origin for a proxied /papi/tv/playlist/ URL.
func (c *TVClient) resolveLegacy(daddyID string) (ResolvedStream, error) {

	referer := c.baseURL + "/"
	resolveURL := c.baseURL + legacyResolvePath + url.PathEscape(daddyID)

	return c.fetchResolvedStream(resolveURL, referer, c.baseURL)

}

// resolveStreamAPI resolves through an optional TV_STREAM_API override endpoint.
func (c *TVClient) resolveStreamAPI(daddyID string) (ResolvedStream, error) {

	api := strings.TrimSpace(os.Getenv("TV_STREAM_API"))
	resolveURL := joinStreamAPI(api, daddyID)
	referer := streamAPIOrigin(api) + "/"

	stream, err := c.fetchResolvedStream(resolveURL, referer, "")

	if err != nil {
		return ResolvedStream{}, err
	}

	if strings.Contains(stream.URL, "cfbu247.sbs") {
		return ResolvedStream{}, fmt.Errorf("cloudflare proxy playlists are not supported")
	}

	return stream, nil

}

func (c *TVClient) fetchResolvedStream(resolveURL, referer, origin string) (ResolvedStream, error) {

	response, err := c.get(resolveURL, referer)

	if err != nil {
		return ResolvedStream{}, fmt.Errorf("resolve stream: %w", err)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)

	if err != nil {
		return ResolvedStream{}, fmt.Errorf("read resolve response: %w", err)
	}

	if response.StatusCode != http.StatusOK {

		msg := strings.TrimSpace(string(body))

		if msg == "" {
			msg = response.Status
		}

		return ResolvedStream{}, fmt.Errorf("resolve stream: status %d: %s", response.StatusCode, msg)

	}

	streamURL, err := parseResolveResponse(body)

	if err != nil {
		return ResolvedStream{}, err
	}

	if streamURL == "" {
		return ResolvedStream{}, fmt.Errorf("resolve failed: empty stream path")
	}

	if !strings.HasPrefix(streamURL, "http://") && !strings.HasPrefix(streamURL, "https://") {

		if origin == "" {
			return ResolvedStream{}, fmt.Errorf("resolve failed: relative stream path without origin")
		}

		if !strings.HasPrefix(streamURL, "/") {
			streamURL = "/" + streamURL
		}

		streamURL = origin + streamURL

	}

	return ResolvedStream{
		URL:     streamURL,
		Referer: referer,
	}, nil

}

func isHLSPlaylistURL(raw string) bool {

	lower := strings.ToLower(strings.TrimSpace(raw))
	path := strings.SplitN(lower, "?", 2)[0]

	if strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".m3u") {
		return true
	}

	return strings.Contains(path, "/papi/tv/playlist/")

}
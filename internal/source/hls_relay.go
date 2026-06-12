package source

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	hlsRelaySegmentTimeout = 8 * time.Second
	hlsRelayPlaylistLimit  = 256 << 10
	hlsRelaySegmentLimit   = 8 << 20
)

// LiveHLSRelay serves a sanitized local HLS playlist for obfuscated live TV sources.
// Upstream playlists often mix MPEG-TS segments (disguised extensions) with PNG slates
// and expired URLs; libav mis-detects those as image video (0x0). The relay only
// forwards transport-stream segments.
type LiveHLSRelay struct {
	upstream string
	headers  map[string]string
	client   *http.Client
	server   *http.Server
	baseURL  string
	listener net.Listener
}

// StartLiveHLSRelay listens on localhost and proxies upstream HLS through TS-only segments.
func StartLiveHLSRelay(ctx context.Context, upstream string, headers map[string]string) (*LiveHLSRelay, error) {

	upstream = strings.TrimSpace(upstream)

	if upstream == "" {
		return nil, fmt.Errorf("live hls relay: upstream url is required")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")

	if err != nil {
		return nil, fmt.Errorf("live hls relay: listen: %w", err)
	}

	relay := &LiveHLSRelay{
		upstream: upstream,
		headers:  cloneHeaders(headers),
		client: &http.Client{
			Timeout: hlsRelaySegmentTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				copyRelayHeaders(req, headers)
				return nil
			},
		},
		listener: listener,
		baseURL:  "http://" + listener.Addr().String(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/play.m3u8", relay.handleRootPlaylist)
	mux.HandleFunc("/playlist", relay.handleNestedPlaylist)
	mux.HandleFunc("/seg", relay.handleSegment)

	relay.server = &http.Server{Handler: mux}

	go func() {

		if err := relay.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("[hls-relay] server stopped: %v", err)
		}

	}()

	go func() {

		<-ctx.Done()
		relay.Close()

	}()

	return relay, nil

}

// URL returns the localhost playlist URL for libavformat.
func (relay *LiveHLSRelay) URL() string {
	return relay.baseURL + "/play.m3u8"
}

// Close stops the relay server.
func (relay *LiveHLSRelay) Close() {

	if relay.server == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = relay.server.Shutdown(ctx)

}

func (relay *LiveHLSRelay) handleRootPlaylist(writer http.ResponseWriter, request *http.Request) {
	relay.servePlaylist(writer, request, relay.upstream)
}

func (relay *LiveHLSRelay) handleNestedPlaylist(writer http.ResponseWriter, request *http.Request) {

	raw, err := decodeRelayURL(request.URL.Query().Get("u"))

	if err != nil {
		http.Error(writer, "bad playlist url", http.StatusBadRequest)
		return
	}

	relay.servePlaylist(writer, request, raw)

}

func (relay *LiveHLSRelay) servePlaylist(writer http.ResponseWriter, request *http.Request, playlistURL string) {

	body, err := relay.fetch(playlistURL)

	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}

	sanitized := rewriteRelayPlaylist(body, playlistURL, relay.baseURL)

	writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write([]byte(sanitized))

}

func (relay *LiveHLSRelay) handleSegment(writer http.ResponseWriter, request *http.Request) {

	raw, err := decodeRelayURL(request.URL.Query().Get("u"))

	if err != nil {
		http.Error(writer, "bad segment url", http.StatusBadRequest)
		return
	}

	body, err := relay.fetch(raw)

	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}

	if !isMPEGTS(body) {
		http.Error(writer, "segment is not transport stream", http.StatusNotFound)
		return
	}

	writer.Header().Set("Content-Type", "video/mp2t")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(body)

}

func (relay *LiveHLSRelay) fetch(rawURL string) ([]byte, error) {

	request, err := http.NewRequest(http.MethodGet, rawURL, nil)

	if err != nil {
		return nil, err
	}

	copyRelayHeaders(request, relay.headers)

	response, err := relay.client.Do(request)

	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream status %d", response.StatusCode)
	}

	limit := hlsRelayPlaylistLimit

	if !strings.Contains(strings.ToLower(rawURL), ".m3u") && !strings.Contains(rawURL, "/playlist") {
		limit = hlsRelaySegmentLimit
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, int64(limit)))

	if err != nil {
		return nil, err
	}

	return body, nil

}

func rewriteRelayPlaylist(body []byte, baseURL, relayBase string) string {

	base, _ := url.Parse(baseURL)
	lines := strings.Split(string(body), "\n")
	out := make([]string, 0, len(lines))

	for _, line := range lines {

		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			out = append(out, line)
			continue
		}

		resolved := resolveRelayURL(trimmed, base)
		encoded := encodeRelayURL(resolved)

		if strings.Contains(strings.ToLower(resolved), ".m3u8") || strings.Contains(resolved, "/playlist") {
			out = append(out, relayBase+"/playlist?u="+encoded)
			continue
		}

		out = append(out, relayBase+"/seg?u="+encoded)

	}

	return strings.Join(out, "\n")

}

func resolveRelayURL(raw string, base *url.URL) string {

	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}

	if base == nil {
		return raw
	}

	resolved, err := base.Parse(raw)

	if err != nil {
		return raw
	}

	return resolved.String()

}

func encodeRelayURL(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeRelayURL(encoded string) (string, error) {

	encoded = strings.TrimSpace(encoded)

	if encoded == "" {
		return "", fmt.Errorf("empty url")
	}

	body, err := base64.RawURLEncoding.DecodeString(encoded)

	if err != nil {
		return "", err
	}

	return string(body), nil

}

func isMPEGTS(body []byte) bool {

	if len(body) < 188 {
		return false
	}

	if len(body) >= 4 && body[0] == 0x89 && body[1] == 'P' && body[2] == 'N' && body[3] == 'G' {
		return false
	}

	if body[0] != 0x47 {
		return false
	}

	if len(body) >= 376 && body[188] != 0x47 {
		return false
	}

	return true

}

func cloneHeaders(headers map[string]string) map[string]string {

	if len(headers) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(headers))

	for key, value := range headers {
		cloned[key] = value
	}

	return cloned

}

func copyRelayHeaders(request *http.Request, headers map[string]string) {

	if len(headers) == 0 {
		return
	}

	for key, value := range headers {
		request.Header.Set(key, value)
	}

}
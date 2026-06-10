package tvapi

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestListChannelsIntegration(t *testing.T) {

	client := NewTVClient(TVOptions{})

	catalog, err := client.ListChannels()

	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}

	if catalog.Total <= 0 {
		t.Fatalf("catalog total = %d, want > 0", catalog.Total)
	}

	if len(catalog.Channels) == 0 {
		t.Fatal("catalog channels is empty")
	}

	if catalog.Channels[0].DaddyID == "" {
		t.Fatal("first channel missing daddyId")
	}

}

func TestResolveHLSIntegration(t *testing.T) {

	client := NewTVClient(TVOptions{})

	catalog, err := client.ListChannels()

	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}

	targets := []string{}

	for _, slug := range []string{"espn-usa", "cnn-usa", "abc-usa", "fox-usa"} {

		if channel, ok := catalog.FindBySlug(slug); ok {
			targets = append(targets, channel.DaddyID)
		}

	}

	if len(targets) == 0 {
		targets = append(targets, catalog.Channels[0].DaddyID)
	}

	for _, daddyID := range targets {

		t.Run("daddyId="+daddyID, func(t *testing.T) {

			hls, err := client.ResolveHLS(daddyID)

			if err != nil {
				t.Fatalf("ResolveHLS: %v", err)
			}

			if !strings.Contains(hls, "/papi/tv/playlist/") && !strings.HasSuffix(hls, ".m3u8") {
				t.Fatalf("unexpected HLS URL: %s", hls)
			}

			playlist, err := fetchPlaylist(hls)

			if err != nil {
				t.Fatalf("fetch playlist: %v", err)
			}

			if !strings.HasPrefix(playlist, "#EXTM3U") {
				t.Fatalf("playlist does not look like HLS: %q", truncate(playlist, 120))
			}

		})

	}

}

func TestResolveHLSRequiresReferer(t *testing.T) {

	base := defaultBaseURL
	daddyID := "609"

	plain, err := http.Get(base + resolvePathPrefix + daddyID)

	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}

	defer plain.Body.Close()

	if plain.StatusCode != http.StatusForbidden {
		t.Fatalf("plain GET status = %d, want 403", plain.StatusCode)
	}

	client := NewTVClient(TVOptions{})

	if _, err := client.ResolveHLS(daddyID); err != nil {
		t.Fatalf("client resolve with Referer: %v", err)
	}

}

func fetchPlaylist(rawURL string) (string, error) {

	client := &http.Client{Timeout: 30 * time.Second}

	request, err := http.NewRequest(http.MethodGet, rawURL, nil)

	if err != nil {
		return "", err
	}

	request.Header.Set("User-Agent", tvBrowserUA)
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")

	if strings.HasPrefix(rawURL, defaultBaseURL) {
		request.Header.Set("Referer", defaultBaseURL+"/")
	}

	response, err := client.Do(request)

	if err != nil {
		return "", err
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))

	if err != nil {
		return "", err
	}

	return string(body), nil

}

func truncate(value string, max int) string {

	if len(value) <= max {
		return value
	}

	return value[:max] + "..."

}
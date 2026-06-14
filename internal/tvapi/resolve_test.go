package tvapi

import (
	"strings"
	"testing"
)

func TestParseResolveResponseLegacy(t *testing.T) {

	body := []byte(`{"success":true,"stream":"/papi/tv/playlist/aHR0cHM6Ly9leGFtcGxlLmNvbS9pbmRleC5tM3U4"}`)

	url, err := parseResolveResponse(body)

	if err != nil {

		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(url, "/papi/tv/playlist/") {

		t.Fatalf("expected legacy playlist path, got %q", url)
	}

}

func TestParseResolveResponseTV247(t *testing.T) {

	body := []byte(`{"channelId":"609","proxyPlaylistUrl":"https://example.com/api/proxy/playlist?token=abc"}`)

	url, err := parseResolveResponse(body)

	if err != nil {

		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(url, "/api/proxy/playlist") {

		t.Fatalf("expected proxy playlist url, got %q", url)
	}

}

func TestIsHLSPlaylistURL(t *testing.T) {

	cases := map[string]bool{

		"https://dami-tv.pro/papi/tv/playlist/abc": true,
		"https://cdn.example.com/live/index.m3u8": true,

		"https://cdn.example.com/live/index.mp4": false,
	}

	for raw, want := range cases {

		if got := isHLSPlaylistURL(raw); got != want {

			t.Fatalf("isHLSPlaylistURL(%q) = %v, want %v", raw, got, want)
		}

	}

}

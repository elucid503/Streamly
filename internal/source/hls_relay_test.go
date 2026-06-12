package source

import (
	"strings"
	"testing"
)

func TestRewriteRelayPlaylist(t *testing.T) {

	body := `#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=3070000
tracks-v1a1/mono.m3u8?md5=abc
#EXTINF:4.0,
https://cdn.example.com/ingest/one.png
`

	rewritten := rewriteRelayPlaylist([]byte(body), "https://dami-tv.pro/papi/tv/playlist/master", "http://127.0.0.1:9")

	if !strings.Contains(rewritten, "http://127.0.0.1:9/playlist?u=") {
		t.Fatalf("expected nested playlist relay url, got %q", rewritten)
	}

	if !strings.Contains(rewritten, "http://127.0.0.1:9/seg?u=") {
		t.Fatalf("expected segment relay url, got %q", rewritten)
	}

}

func TestIsMPEGTS(t *testing.T) {

	ts := make([]byte, 376)
	ts[0] = 0x47
	ts[188] = 0x47

	if !isMPEGTS(ts) {
		t.Fatal("expected ts sync detection")
	}

	png := make([]byte, 188)
	copy(png, []byte{0x89, 'P', 'N', 'G'})

	if isMPEGTS(png) {
		t.Fatal("png must not register as ts")
	}

}
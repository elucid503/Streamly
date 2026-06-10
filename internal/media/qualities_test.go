package media

import (
	"testing"

	"streamly/internal/febapi"
)

func TestRankedQualityURLs(t *testing.T) {

	qualities := []febapi.FileQuality{
		{URL: "https://example.com/low.mp4", Quality: "480p", Name: "480p.mp4"},
		{URL: "https://example.com/high.mp4", Quality: "1080p", Name: "1080p.x264.mkv"},
		{URL: "https://example.com/hls.m3u8", Quality: "1080p", Name: "hls"},
		{URL: "https://example.com/high.mp4", Quality: "1080p", Name: "dup"},
	}

	urls := RankedQualityURLs(qualities, 720)

	if len(urls) != 2 {
		t.Fatalf("expected 2 progressive urls, got %v", urls)
	}

	if urls[0] != "https://example.com/high.mp4" {
		t.Fatalf("expected 1080p first, got %q", urls[0])
	}

	if urls[1] != "https://example.com/low.mp4" {
		t.Fatalf("expected 480p fallback, got %q", urls[1])
	}

}

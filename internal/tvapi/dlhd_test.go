package tvapi

import (
	"strings"
	"testing"
)

func TestExtractAtobSource(t *testing.T) {

	page := `source: window.atob('aHR0cHM6Ly92b21vcy5waGFudGVtbGlzLnRvcC9wcmVtaXVtNTEvaW5kZXgubTN1OD9leHBpcmVzPTEyMw=='),`

	url, ok := extractAtobSource(page)

	if !ok {

		t.Fatal("expected atob source")
	}

	if !strings.Contains(url, "phantemlis.top/premium51/index.m3u8") {

		t.Fatalf("unexpected url %q", url)
	}

}

func TestResolveDLHD(t *testing.T) {

	client := NewTVClient(TVOptions{})

	stream, err := client.resolveDLHD("51")

	if err != nil {

		t.Fatalf("resolve dlhd: %v", err)
	}

	if !isHLSPlaylistURL(stream.URL) {

		t.Fatalf("expected hls playlist url, got %q", stream.URL)
	}

	if stream.Referer == "" {

		t.Fatal("expected embed referer")
	}

}

func TestResolveStreamUsesDLHD(t *testing.T) {

	client := NewTVClient(TVOptions{})

	stream, err := client.ResolveStream("44")

	if err != nil {

		t.Fatalf("resolve stream: %v", err)
	}

	if !strings.Contains(stream.URL, ".m3u8") {

		t.Fatalf("expected direct cdn playlist, got %q", stream.URL)
	}

	if stream.Referer == "" {

		t.Fatal("expected embed referer")
	}

}

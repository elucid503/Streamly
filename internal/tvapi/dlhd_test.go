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

func TestResolveDLHDABC(t *testing.T) {

	client := NewTVClient(TVOptions{})

	stream, err := client.ResolveStream("51")

	if err != nil {
		t.Fatalf("resolve stream: %v", err)
	}

	if !strings.Contains(stream.URL, ".m3u8") {
		t.Fatalf("expected m3u8 url, got %q", stream.URL)
	}

	if !strings.Contains(stream.URL, "phantemlis.top") {
		t.Fatalf("expected direct cdn url, got %q", stream.URL)
	}

	if stream.Referer == "" {
		t.Fatal("expected embed referer")
	}

}
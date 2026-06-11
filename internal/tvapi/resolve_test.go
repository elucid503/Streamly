package tvapi

import (
	"strings"
	"testing"
)

func TestParseResolveResponseTV247(t *testing.T) {

	body := []byte(`{"channelId":"609","proxyPlaylistUrl":"https://chat.cfbu247.sbs/api/proxy/playlist?token=abc"}`)

	url, err := parseResolveResponse(body)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(url, "/api/proxy/playlist") {
		t.Fatalf("expected proxy playlist url, got %q", url)
	}

}
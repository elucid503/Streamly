package tvapi

import "testing"

func TestExtractCdnlivePlaylist(t *testing.T) {

	page := `function Rok(s){s=s.replace(/-/g,'+').replace(/_/g,'/');while(s.length%4)s+='=';return atob(s)};` +
		`var A='aHR0cHM';var B='Og';var C='Ly9leGFtcGxlLmNvbS9wbGF5bGlzdC5tM3U4';` +
		`var STREAM=Rok(A)+Rok(B)+Rok(C);`

	url, ok := extractCdnlivePlaylist(page)

	if !ok {

		t.Fatal("expected playlist url")

	}

	if url != "https://example.com/playlist.m3u8" {

		t.Fatalf("got %q", url)

	}

}

func TestBroadcasterFromSourceLabel(t *testing.T) {

	got := broadcasterFromSourceLabel("Server Kobra - ADMIN - English - NBC Sports BA - Stream 1 [HD]")

	if got != "NBC Sports BA" {

		t.Fatalf("got %q", got)

	}

	if broadcasterFromSourceLabel("Server Kobra - ECHO") != "" {

		t.Fatal("expected empty for source-only label")

	}

}

func TestParseSourceSelectBroadcasters(t *testing.T) {

	page := `<select id="sourceSelect"><option>Server Kobra - ADMIN - English - ESPN - Stream 1</option><option>Server Kobra - ECHO</option></select>`
	names := parseSourceSelectBroadcasters(page)

	if len(names) != 1 || names[0] != "ESPN" {

		t.Fatalf("got %#v", names)

	}

}

func TestIsHLSPlaylistURL(t *testing.T) {

	if !isHLSPlaylistURL("https://cdn.example/x/playlist.m3u8?token=1") {

		t.Fatal("expected true")

	}

	if isHLSPlaylistURL("https://example.com/index.html") {

		t.Fatal("expected false")

	}

}

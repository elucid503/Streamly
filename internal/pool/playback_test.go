package pool

import (
	"strings"
	"testing"

	"streamly/internal/media"
	"streamly/internal/transcode"
)

func TestWrapPauseBody(t *testing.T) {

	if lines := wrapPauseBody("", 20, 3); lines != nil {
		t.Fatalf("empty text must yield no lines, got %v", lines)
	}

	lines := wrapPauseBody("one two three four five six seven eight nine ten", 12, 3)

	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %v", lines)
	}

	if !strings.HasSuffix(lines[2], "...") {
		t.Fatalf("truncated wrap must end with ellipsis, got %q", lines[2])
	}

	for _, line := range lines {
		if len([]rune(line)) > 12 {
			t.Fatalf("line %q exceeds width", line)
		}
	}

	short := wrapPauseBody("just fits", 20, 3)

	if len(short) != 1 || short[0] != "just fits" {
		t.Fatalf("short text must stay on one untruncated line, got %v", short)
	}

	long := wrapPauseBody("supercalifragilisticexpialidocious", 10, 2)

	if len(long) != 1 || len([]rune(long[0])) > 10 {
		t.Fatalf("overlong word must be hard-cut to width, got %v", long)
	}

}

func TestBuildPauseCard(t *testing.T) {

	session := &Session{ctaFontPath: "/tmp/font.ttf"}

	card := session.buildPauseCard("Fallback Caption")

	if card.Title != "Fallback Caption" || card.CTA != PauseCTAText || card.FontPath != "/tmp/font.ttf" {
		t.Fatalf("unexpected fallback card: %+v", card)
	}

	session.Metadata = &StreamMetadata{
		Details: media.TitleDetails{
			Title:         "Show Name",
			Description:   "A description.",
			EpisodeTitles: map[string]string{"2:5": "The One"},
		},
		Episode: &EpisodeRef{Season: 2, Episode: 5},
	}

	card = session.buildPauseCard("Fallback Caption")

	if card.Title != "Show Name" {
		t.Fatalf("expected metadata title, got %q", card.Title)
	}

	if card.Subtitle != "S2E5 — The One" {
		t.Fatalf("unexpected subtitle %q", card.Subtitle)
	}

	if len(card.BodyLines) == 0 {
		t.Fatal("expected description lines")
	}

	empty := (&Session{}).buildPauseCard("")

	if empty.Title != "Paused" {
		t.Fatalf("empty card must fall back to Paused, got %q", empty.Title)
	}

}

func TestEnrichLiveTranscodeUsesLoadingCard(t *testing.T) {

	session := &Session{ctaFontPath: "/tmp/font.ttf"}
	treq := transcode.Request{Live: true, Caption: "Live Channel"}

	session.enrichTranscodeRequest(&treq, 0)

	if treq.PauseCard == nil {
		t.Fatal("live transcode must carry a loading card")
	}

	if treq.PauseCard.CTA != LoadingCTAText || treq.PauseCard.Title != "Live Channel" {
		t.Fatalf("unexpected loading card: %+v", treq.PauseCard)
	}

}

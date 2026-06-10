package captions

import (
	"testing"
	"unicode/utf8"
)

func TestNormalizeSubtitleUTF8(t *testing.T) {

	utf8Input := []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n")
	got := normalizeSubtitleUTF8(utf8Input)

	if !utf8.Valid(got) || string(got) != string(utf8Input) {
		t.Fatalf("expected valid unchanged utf8, got %q", got)
	}

	bomInput := append([]byte{0xEF, 0xBB, 0xBF}, utf8Input...)
	got = normalizeSubtitleUTF8(bomInput)

	if !utf8.Valid(got) || string(got) != string(utf8Input) {
		t.Fatalf("expected bom strip, got %q", got)
	}

	latin1 := []byte("1\n00:00:01,000 --> 00:00:02,000\nCaf\xe9\n")
	got = normalizeSubtitleUTF8(latin1)

	if !utf8.Valid(got) {
		t.Fatalf("expected valid utf8 after latin1 conversion, got %q", got)
	}

	if string(got) != "1\n00:00:01,000 --> 00:00:02,000\nCafé\n" {
		t.Fatalf("unexpected latin1 conversion: %q", got)
	}

}
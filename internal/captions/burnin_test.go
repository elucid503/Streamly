package captions

import (
	"os"
	"testing"
	"unicode/utf8"
)

func TestWriteBurnInSubtitle(t *testing.T) {

	path := t.TempDir() + "/subs.srt"

	_, err := WriteBurnInSubtitle(path, []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))

	if err != nil {
		t.Fatalf("WriteBurnInSubtitle: %v", err)
	}

	data, err := os.ReadFile(path)

	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if !utf8.Valid(data) {
		t.Fatal("expected valid utf8")
	}

}

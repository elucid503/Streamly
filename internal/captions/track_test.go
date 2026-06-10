package captions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrackEnableDisableReset(t *testing.T) {

	dir := t.TempDir()
	path := filepath.Join(dir, "test.srt")

	if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\nHi\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	track := &Track{}
	track.Set(path)

	if !track.Enabled() || track.Path() != path || !track.HasSubtitle() {
		t.Fatalf("expected enabled track with path %q", path)
	}

	track.Disable()

	if track.Enabled() || track.Path() != "" {
		t.Fatal("expected disabled track")
	}

	if track.StoredPath() != path {
		t.Fatalf("expected cached path %q, got %q", path, track.StoredPath())
	}

	track.Enable()

	if !track.Enabled() {
		t.Fatal("expected re-enabled track")
	}

	track.Reset()

	if track.HasSubtitle() || track.Enabled() {
		t.Fatal("expected reset track")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected subtitle file removed on reset")
	}

}
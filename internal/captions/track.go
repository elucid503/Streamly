package captions

import (
	"os"
	"sync"
)

// Track holds the active subtitle file and whether burn-in is enabled.
type Track struct {
	mu      sync.Mutex
	enabled bool
	path    string
}

// Enabled reports whether captions should be burned into the video stream.
func (t *Track) Enabled() bool {

	if t == nil {
		return false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.enabled && t.path != ""

}

// StoredPath returns the cached subtitle file regardless of enabled state.
func (t *Track) StoredPath() string {

	if t == nil {
		return ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.path

}

// HasSubtitle reports whether a subtitle file is cached on disk.
func (t *Track) HasSubtitle() bool {

	return t.StoredPath() != ""

}

// Path returns the on-disk subtitle file used when enabled.
func (t *Track) Path() string {

	if t == nil {
		return ""
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.enabled {
		return ""
	}

	return t.path

}

// Set stores a subtitle file and enables burn-in.
func (t *Track) Set(path string) {

	t.mu.Lock()
	defer t.mu.Unlock()

	t.path = path
	t.enabled = path != ""

}

// Enable turns burn-in on for the stored subtitle file.
func (t *Track) Enable() {

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.path != "" {
		t.enabled = true
	}

}

// Disable turns burn-in off without deleting the cached subtitle file.
func (t *Track) Disable() {

	t.mu.Lock()
	defer t.mu.Unlock()

	t.enabled = false

}

// Reset disables burn-in and removes the cached subtitle file.
func (t *Track) Reset() {

	t.mu.Lock()
	path := t.path
	t.path = ""
	t.enabled = false
	t.mu.Unlock()

	if path != "" {
		_ = os.Remove(path)
	}

}

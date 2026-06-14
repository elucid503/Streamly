package captions

import (
	"os"
	"sync"
)

type Track struct {

	mu sync.Mutex

	enabled bool
	path string

}

func (t *Track) Enabled() bool {

	if t == nil {

		return false

	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.enabled && t.path != ""

}

func (t *Track) StoredPath() string {

	if t == nil {

		return ""

	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return t.path

}

func (t *Track) HasSubtitle() bool {

	return t.StoredPath() != ""

}

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

func (t *Track) Set(path string) {

	t.mu.Lock()
	defer t.mu.Unlock()

	t.path = path
	t.enabled = path != ""

}

func (t *Track) Enable() {

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.path != "" {

		t.enabled = true

	}

}

func (t *Track) Disable() {

	t.mu.Lock()
	defer t.mu.Unlock()

	t.enabled = false

}

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

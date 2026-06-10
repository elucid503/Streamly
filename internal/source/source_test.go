package source

// Verifies the Range-seeking MediaSource against a real HTTP server, including the
// jump-to-trailing-bytes pattern libavformat uses to probe MP4 indexes.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMediaSourceSeekRead(t *testing.T) {

	payload := make([]byte, 1<<20)

	for i := range payload {
		payload[i] = byte(i % 251)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "sample.bin", time.Now(), bytes.NewReader(payload))
	}))

	defer server.Close()

	media, err := Create(nil, nil, server.URL+"/sample.bin", nil)

	if err != nil {
		t.Fatalf("create: %v", err)
	}

	defer media.Destroy()

	if size := media.Size(); size != int64(len(payload)) {
		t.Fatalf("size = %d, want %d", size, len(payload))
	}

	// Sequential read from the start.
	head := make([]byte, 4096)

	if _, err := io.ReadFull(media, head); err != nil {
		t.Fatalf("read head: %v", err)
	}

	if !bytes.Equal(head, payload[:4096]) {
		t.Fatalf("head bytes mismatch")
	}

	// Jump near the end like an MP4 trailing-moov probe.
	target := int64(len(payload) - 1000)

	if pos, err := media.Seek(target, io.SeekStart); err != nil || pos != target {
		t.Fatalf("seek: pos=%d err=%v", pos, err)
	}

	tail := make([]byte, 1000)

	if _, err := io.ReadFull(media, tail); err != nil {
		t.Fatalf("read tail: %v", err)
	}

	if !bytes.Equal(tail, payload[target:]) {
		t.Fatalf("tail bytes mismatch")
	}

	// EOF after the last byte.
	if n, err := media.Read(make([]byte, 16)); err != io.EOF || n != 0 {
		t.Fatalf("expected EOF, got n=%d err=%v", n, err)
	}

	// Seek back to the middle and verify again.
	if _, err := media.Seek(500000, io.SeekStart); err != nil {
		t.Fatalf("seek middle: %v", err)
	}

	middle := make([]byte, 8192)

	if _, err := io.ReadFull(media, middle); err != nil {
		t.Fatalf("read middle: %v", err)
	}

	if !bytes.Equal(middle, payload[500000:500000+8192]) {
		t.Fatalf("middle bytes mismatch")
	}

}

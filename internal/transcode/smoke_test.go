//go:build cgo

package transcode

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTranscodeSmokeFile(t *testing.T) {

	path := os.Getenv("STREAMLY_TEST_MEDIA")

	if path == "" {
		path = "/tmp/streamly_test.mp4"
	}

	if _, err := os.Stat(path); err != nil {
		t.Skip("set STREAMLY_TEST_MEDIA or place a sample file at /tmp/streamly_test.mp4")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	session, err := Start(Request{
		InputURL: path,
		Context:  ctx,
	})

	if err != nil {
		t.Fatalf("transcode start: %v", err)
	}

	var videoPackets atomic.Int32
	var audioPackets atomic.Int32

	var drain sync.WaitGroup
	drain.Add(2)

	go func() {

		defer drain.Done()

		for range session.Video {
			videoPackets.Add(1)
		}

	}()

	go func() {

		defer drain.Done()

		for range session.Audio {
			audioPackets.Add(1)
		}

	}()

	select {
	case err := <-session.Done:
		if err != nil && ctx.Err() == nil {
			t.Fatalf("transcode done: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("transcode timed out with %d video / %d audio packets", videoPackets.Load(), audioPackets.Load())
	}

	drain.Wait()

	if videoPackets.Load() == 0 {
		t.Fatal("expected at least one video packet")
	}

	if audioPackets.Load() == 0 {
		t.Fatal("expected at least one audio packet")
	}

	TrimNativeHeap()

}
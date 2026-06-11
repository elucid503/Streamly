//go:build cgo

package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type fileInput struct {
	*os.File
}

func (f fileInput) Size() int64 {

	info, err := f.Stat()

	if err != nil {
		return -1
	}

	return info.Size()

}

// TestPauseFrameEndToEnd transcodes a generated clip, pauses, and renders the pause
// card through the native path: freeze-ring pick, overlay filter graph, IDR encode.
func TestPauseFrameEndToEnd(t *testing.T) {

	if testing.Short() {
		t.Skip("end-to-end transcode test")
	}

	ffmpeg, err := exec.LookPath("ffmpeg")

	if err != nil {
		t.Skip("ffmpeg not available to generate the test clip")
	}

	clip := filepath.Join(t.TempDir(), "clip.mp4")

	gen := exec.Command(ffmpeg, "-y", "-f", "lavfi", "-i", "testsrc=duration=4:size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", clip)

	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate clip: %v\n%s", err, out)
	}

	file, err := os.Open(clip)

	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	fontPath, err := filepath.Abs(filepath.Join("..", "..", "assets", "font.ttf"))

	if err != nil || !fileExists(fontPath) {
		t.Skipf("font not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := Start(Request{
		Source:  fileInput{file},
		Caption: "pause-card-test",
		Context: ctx,
		PauseCard: &PauseCard{
			Title:     "Test Show",
			Subtitle:  "Season 1, Episode 2 - Pilot",
			BodyLines: []string{"A description line that sits", "on the pause card."},
			CTA:       "Use /resume to resume playback.",
			FontPath:  fontPath,
		},
	})

	if err != nil {
		t.Fatalf("start transcode: %v", err)
	}

	var lastPTS time.Duration

	for i := 0; i < 10; i++ {

		select {
		case packet, ok := <-session.Video:

			if !ok {
				t.Fatal("video feed closed before any packets")
			}

			lastPTS = packet.PTS

		case <-ctx.Done():
			t.Fatal("timed out waiting for video packets")
		}

	}

	if frame, ok := session.PauseFrame(lastPTS.Milliseconds()); ok || frame != nil {
		t.Fatal("PauseFrame must refuse while not paused")
	}

	session.Pause()

	frame, ok := session.PauseFrame(lastPTS.Milliseconds())

	if !ok || len(frame) == 0 {
		t.Fatal("expected an encoded pause frame while paused")
	}

	if !annexBHasNAL(frame, 5) || !annexBHasNAL(frame, 7) || !annexBHasNAL(frame, 8) {
		t.Fatalf("pause frame must be a self-contained IDR with SPS/PPS; got %d bytes", len(frame))
	}

	cached, ok := session.PauseFrame(lastPTS.Milliseconds())

	if !ok || &cached[0] != &frame[0] {
		t.Fatal("second PauseFrame in the same pause must return the cached frame")
	}

	session.Resume()
	cancel()

	select {
	case <-session.Done:
	case <-time.After(10 * time.Second):
		t.Fatal("transcode did not shut down")
	}

}

func fileExists(path string) bool {

	_, err := os.Stat(path)

	return err == nil

}

// annexBHasNAL scans an Annex-B buffer for a NAL unit of the given type.
func annexBHasNAL(buf []byte, nalType byte) bool {

	for i := 0; i+3 < len(buf); i++ {

		if buf[i] == 0 && buf[i+1] == 0 && buf[i+2] == 1 {

			if buf[i+3]&0x1f == nalType {
				return true
			}

			i += 2

		}

	}

	return false

}

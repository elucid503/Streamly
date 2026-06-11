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

// TestDumpPauseCard writes the rendered pause card to /tmp for visual inspection.
func TestDumpPauseCard(t *testing.T) {

	if os.Getenv("DUMP_PAUSE_CARD") == "" {
		t.Skip("set DUMP_PAUSE_CARD=1 to render the card to /tmp/pause_card.png")
	}

	ffmpeg, err := exec.LookPath("ffmpeg")

	if err != nil {
		t.Skip("ffmpeg not available")
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

	fontPath, _ := filepath.Abs(filepath.Join("..", "..", "assets", "font.ttf"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := Start(Request{
		Source:  fileInput{file},
		Caption: "pause-card-dump",
		Context: ctx,
		PauseCard: &PauseCard{
			Title:    "Severance",
			Subtitle: "Season 2, Episode 7 - Chikhai Bardo",
			BodyLines: []string{
				"Mark S. balances work and life at Lumon Industries, where",
				"employees undergo a procedure that surgically divides their",
				"memories between their work and personal lives. A daring",
				"experiment in work-life balance begins to unravel...",
			},
			CTA:      "Use /resume to resume playback.",
			FontPath: fontPath,
		},
	})

	if err != nil {
		t.Fatalf("start transcode: %v", err)
	}

	var lastPTS time.Duration

	for i := 0; i < 40; i++ {
		packet := <-session.Video
		lastPTS = packet.PTS
	}

	session.Pause()

	frame, ok := session.PauseFrame(lastPTS.Milliseconds())

	if !ok {
		t.Fatal("no pause frame")
	}

	raw := "/tmp/pause_card.h264"

	if err := os.WriteFile(raw, frame, 0o644); err != nil {
		t.Fatal(err)
	}

	conv := exec.Command(ffmpeg, "-y", "-i", raw, "-frames:v", "1", "/tmp/pause_card.png")

	if out, err := conv.CombinedOutput(); err != nil {
		t.Fatalf("convert: %v\n%s", err, out)
	}

	cancel()
	<-session.Done

}

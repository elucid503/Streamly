package captions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
)

func TestSopranosDownloadedContentIsEnglish(t *testing.T) {
	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	apiKey := os.Getenv("SUBDL_API_KEY")
	if apiKey == "" {
		t.Skip("no SUBDL_API_KEY")
	}

	client := NewSubDLClient(SubDLOptions{APIKey: apiKey})
	dest := filepath.Join(t.TempDir(), "sopranos.srt")

	_, err := client.Download(context.Background(), Query{
		IMDBId:    "tt0141842",
		TMDBId:    1398,
		Season:    2,
		Episode:   10,
		VideoName: "The.Sopranos.S02E10.1080p.5.1Ch.BluRay.ReEnc-DeeJayAhmed.mkv",
	}, dest)
	if err != nil {
		t.Fatalf("download: %v", err)
	}

	data, _ := os.ReadFile(dest)
	t.Logf("bytes=%d english=%v preview=%q", len(data), looksEnglishSubtitle(data), string(data[:min(len(data), 400)]))
	if !looksEnglishSubtitle(data) {
		t.Fatal("sopranos s02e10 subtitle is not english")
	}
}

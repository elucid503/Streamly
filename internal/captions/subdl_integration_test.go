package captions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

func TestSubDLIntegration(t *testing.T) {

	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	apiKey := os.Getenv("SUBDL_API_KEY")

	if apiKey == "" {
		t.Skip("set SUBDL_API_KEY to run SubDL integration tests")
	}

	client := NewSubDLClient(SubDLOptions{APIKey: apiKey})

	dest := filepath.Join(t.TempDir(), "inception.srt")

	source, err := client.Download(context.Background(), Query{
		IMDBId: "tt1375666",
		TMDBId: 27205,
	}, dest)

	if err != nil {
		t.Fatalf("Download inception: %v", err)
	}

	if source != "SubDL" {
		t.Fatalf("expected source SubDL, got %q", source)
	}

	data, err := os.ReadFile(dest)

	if err != nil {
		t.Fatalf("read subtitle file: %v", err)
	}

	if !looksLikeSubtitle(data) {
		t.Fatalf("downloaded payload does not look like subtitles: %q", string(data[:min(len(data), 120)]))
	}

}

func TestSubDLEpisodePickIntegration(t *testing.T) {

	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	apiKey := os.Getenv("SUBDL_API_KEY")

	if apiKey == "" {
		t.Skip("set SUBDL_API_KEY to run SubDL integration tests")
	}

	client := NewSubDLClient(SubDLOptions{APIKey: apiKey})

	base := Query{
		IMDBId: "tt0903747",
		TMDBId: 1396,
		Season: 1,
	}

	cases := []struct {
		episode int
		avoid   []string
	}{
		{1, nil},
		{3, []string{"pilot", "Kw4BFXfSlK"}},
		{5, []string{"pilot", "Kw4BFXfSlK", "bnSjf27w28"}},
	}

	for _, tc := range cases {

		tc := tc
		t.Run(fmt.Sprintf("s01e%02d", tc.episode), func(t *testing.T) {

			query := base
			query.Episode = tc.episode

			path, err := client.SearchPath(context.Background(), query)

			if err != nil {
				t.Fatalf("SearchPath s01e%02d: %v", tc.episode, err)
			}

			lower := strings.ToLower(path)

			for _, token := range tc.avoid {
				if strings.Contains(lower, strings.ToLower(token)) {
					t.Fatalf("episode %d picked wrong path %q (matched %q)", tc.episode, path, token)
				}
			}

			dest := filepath.Join(t.TempDir(), fmt.Sprintf("s01e%02d.srt", tc.episode))

			if _, err := client.Download(context.Background(), query, dest); err != nil {
				t.Fatalf("Download s01e%02d: %v", tc.episode, err)
			}

			data, err := os.ReadFile(dest)

			if err != nil {
				t.Fatalf("read subtitle file: %v", err)
			}

			if !looksLikeSubtitle(data) {
				t.Fatalf("downloaded payload does not look like subtitles")
			}

		})

	}

}

func TestSubDLSopranosS02E10Integration(t *testing.T) {

	_ = godotenv.Load("../../.env")
	_ = godotenv.Load(".env")

	apiKey := os.Getenv("SUBDL_API_KEY")

	if apiKey == "" {
		t.Skip("set SUBDL_API_KEY to run SubDL integration tests")
	}

	client := NewSubDLClient(SubDLOptions{APIKey: apiKey})

	dest := filepath.Join(t.TempDir(), "sopranos-s02e10.srt")

	source, err := client.Download(context.Background(), Query{
		IMDBId:    "tt0141842",
		TMDBId:    1398,
		Season:    2,
		Episode:   10,
		VideoName: "The.Sopranos.S02E10.1080p.5.1Ch.BluRay.ReEnc-DeeJayAhmed.mkv",
	}, dest)

	if err != nil {
		t.Fatalf("Download sopranos s02e10: %v", err)
	}

	if source != "SubDL" {
		t.Fatalf("expected source SubDL, got %q", source)
	}

	data, err := os.ReadFile(dest)

	if err != nil {
		t.Fatalf("read subtitle file: %v", err)
	}

	if !looksLikeSubtitle(data) {
		t.Fatalf("downloaded payload does not look like subtitles")
	}

}
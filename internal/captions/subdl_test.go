package captions

import "testing"

func TestImdbQueryID(t *testing.T) {

	if got := imdbQueryID("1375666"); got != "tt1375666" {
		t.Fatalf("expected tt1375666, got %q", got)
	}

	if got := imdbQueryID("tt1375666"); got != "tt1375666" {
		t.Fatalf("expected tt1375666, got %q", got)
	}

}

func TestParseEpisodeTag(t *testing.T) {

	season, episode := parseEpisodeTag("Breaking.Bad.S01E07.A.No-Rough-Stuff-Type.Deal")

	if season != 1 || episode != 7 {
		t.Fatalf("expected 1x7, got %dx%d", season, episode)
	}

}

func TestParseLeadingEpisode(t *testing.T) {

	if got := parseLeadingEpisode("03 ...And the Bag's in the River.en.srt"); got != 3 {
		t.Fatalf("expected episode 3, got %d", got)
	}

	if got := parseLeadingEpisode("01 Pilot.en.srt"); got != 1 {
		t.Fatalf("expected episode 1, got %d", got)
	}

}

func TestPickSubDLDownloadSeasonPack(t *testing.T) {

	response := subdlSearchResponse{
		Subtitles: []subdlSubtitle{
			{
				ReleaseName: "Breaking Bad, SEASON 1 1080p x265",
				UnpackFiles: []subdlUnpackFile{
					{URL: "/subtitle/pack/pilot", Name: "01 Pilot.en.srt", Season: 0, Episode: 0},
					{URL: "/subtitle/pack/ep3", Name: "03 ...And the Bag's in the River.en.srt", Season: 1, Episode: 0},
					{URL: "/subtitle/pack/ep5", Name: "05 Gray Matter.en.srt", Season: 0, Episode: 5},
				},
			},
		},
	}

	if got := pickSubDLDownload(response, 1, 3); got != "/subtitle/pack/ep3" {
		t.Fatalf("expected episode 3 path, got %q", got)
	}

	if got := pickSubDLDownload(response, 1, 5); got != "/subtitle/pack/ep5" {
		t.Fatalf("expected episode 5 path, got %q", got)
	}

	if got := pickSubDLDownload(response, 1, 1); got != "/subtitle/pack/pilot" {
		t.Fatalf("expected episode 1 path, got %q", got)
	}

}

func TestPickSubDLSeasonZipFallback(t *testing.T) {

	response := subdlSearchResponse{
		Subtitles: []subdlSubtitle{
			{
				ReleaseName: "The.Sopranos.S02.BluRay.X264-REWARD",
				Season:      2,
				URL:         "/subtitle/3365257-2866354.zip",
			},
			{
				ReleaseName: "TheSopranosS02Complete(EngForcedOnly)",
				Name:        "the-sopranos-second-season_2-english-3219271.zip",
				Season:      2,
				URL:         "/subtitle/3202829-3219271.zip",
			},
		},
	}

	paths := pickSubDLPaths(response, 2, 10)

	if len(paths) == 0 {
		t.Fatal("expected season zip fallback paths")
	}

	if paths[0] != "/subtitle/3365257-2866354.zip" {
		t.Fatalf("expected full season pack first, got %q", paths[0])
	}

}

func TestExtractFromZipEpisode(t *testing.T) {

	data := buildTestZip(t, map[string]string{
		"The.Sopranos.S02E09.BluRay.srt": "9",
		"The.Sopranos.S02E10.BluRay.srt": "10",
	})

	payload, err := extractFromZip(data, 2, 10)

	if err != nil {
		t.Fatalf("extractFromZip: %v", err)
	}

	if string(payload) != "10" {
		t.Fatalf("expected episode 10 payload, got %q", string(payload))
	}

}

func TestPickSubDLDownloadSingleEpisodeRelease(t *testing.T) {

	response := subdlSearchResponse{
		Subtitles: []subdlSubtitle{
			{
				ReleaseName: "Breaking Bad.S01E07.A No-Rough-Stuff-Type Deal",
				Episode:     7,
				Season:      1,
				UnpackFiles: []subdlUnpackFile{
					{URL: "/subtitle/single/ep7", Name: "Breaking Bad.S01E07.en.srt"},
				},
			},
		},
	}

	if got := pickSubDLDownload(response, 1, 7); got != "/subtitle/single/ep7" {
		t.Fatalf("expected episode 7 path, got %q", got)
	}

}

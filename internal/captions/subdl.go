package captions

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/imroc/req/v3"
)

const (
	subDLBaseURL   = "https://api.subdl.com/api/v1"
	subDLDownload  = "https://dl.subdl.com"
	subDLUserAgent = "Streamly v1.0"
)

var (
	episodeTagRE     = regexp.MustCompile(`(?i)(?:^|[.\s_-])s(\d{1,2})e(\d{1,2})(?:[.\s_-]|$)`)
	episodeXRE       = regexp.MustCompile(`(?i)(?:^|[.\s_-])(\d{1,2})x(\d{1,2})(?:[.\s_-]|$)`)
	leadingEpisodeRE = regexp.MustCompile(`(?i)^(\d{1,2})\s+`)
)

// SubDLOptions tunes the SubDL client.
type SubDLOptions struct {
	APIKey string
}

// SubDLClient fetches English subtitles via the SubDL search API.
type SubDLClient struct {
	apiKey string
	http   *req.Client
}

type subdlUnpackFile struct {
	URL         string `json:"url"`
	Name        string `json:"name"`
	ReleaseName string `json:"release_name"`
	Season      int    `json:"season"`
	Episode     int    `json:"episode"`
	Format      string `json:"format"`
	Language    string `json:"language"`
	Hi          bool   `json:"hi"`
}

type subdlSubtitle struct {
	ReleaseName string            `json:"release_name"`
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	Season      int               `json:"season"`
	Episode     int               `json:"episode"`
	Hi          bool              `json:"hi"`
	UnpackFiles []subdlUnpackFile `json:"unpack_files"`
}

type subdlSearchResponse struct {
	Status    bool            `json:"status"`
	Error     string          `json:"error"`
	Subtitles []subdlSubtitle `json:"subtitles"`
}

func NewSubDLClient(options SubDLOptions) *SubDLClient {

	return &SubDLClient{
		apiKey: strings.TrimSpace(options.APIKey),
		http: req.C().
			SetTimeout(20 * time.Second).
			SetUserAgent(subDLUserAgent).
			ImpersonateChrome(),
	}

}

func (c *SubDLClient) Name() string {

	return "SubDL"

}

func (c *SubDLClient) Configured() bool {

	return c.apiKey != ""

}

func (c *SubDLClient) Download(ctx context.Context, query Query, destPath string) (string, error) {

	if !c.Configured() {
		return "", ErrUnconfigured
	}

	paths, err := c.resolvePaths(ctx, query)

	if err != nil {
		return "", err
	}

	var lastErr error

	for _, downloadPath := range paths {

		data, err := c.downloadBytes(ctx, downloadPath)

		if err != nil {
			lastErr = err
			continue
		}

		payload, err := extractSubtitle(data, query.Season, query.Episode)

		if err != nil {
			lastErr = err
			continue
		}

		if !looksEnglishSubtitle(payload) {
			lastErr = ErrNoSubtitle
			continue
		}

		if _, err := WriteBurnInSubtitle(destPath, payload); err != nil {
			return "", err
		}

		return c.Name(), nil

	}

	if lastErr != nil {
		return "", lastErr
	}

	return "", ErrNoSubtitle

}

// SearchPath resolves the best SubDL download path for a query.
func (c *SubDLClient) SearchPath(ctx context.Context, query Query) (string, error) {

	if !c.Configured() {
		return "", ErrUnconfigured
	}

	paths, err := c.resolvePaths(ctx, query)

	if err != nil {
		return "", err
	}

	if len(paths) == 0 {
		return "", ErrNoSubtitle
	}

	return paths[0], nil

}

func (c *SubDLClient) resolvePaths(ctx context.Context, query Query) ([]string, error) {

	params := url.Values{}
	params.Set("api_key", c.apiKey)
	params.Set("languages", "EN")
	params.Set("unpack", "1")

	if query.Season > 0 && query.Episode > 0 {
		params.Set("type", "tv")
		params.Set("season_number", strconv.Itoa(query.Season))
		params.Set("episode_number", strconv.Itoa(query.Episode))
	} else {
		params.Set("type", "movie")
	}

	if imdb := imdbQueryID(query.IMDBId); imdb != "" {
		params.Set("imdb_id", imdb)
	} else if query.TMDBId > 0 {
		params.Set("tmdb_id", strconv.Itoa(query.TMDBId))
	} else {
		return nil, ErrNoSubtitle
	}

	if name := strings.TrimSpace(query.VideoName); name != "" {
		params.Set("file_name", name)
	}

	var response subdlSearchResponse

	resp, err := c.http.R().
		SetContext(ctx).
		SetSuccessResult(&response).
		Get(subDLBaseURL + "/subtitles?" + params.Encode())

	if err != nil {
		return nil, err
	}

	if resp.IsErrorState() {
		return nil, mapSubDLError(resp.StatusCode, resp.String())
	}

	if !response.Status {
		if strings.TrimSpace(response.Error) != "" {
			return nil, fmt.Errorf("captions: subdl: %s", strings.TrimSpace(response.Error))
		}
		return nil, ErrNoSubtitle
	}

	if paths := pickSubDLPaths(response, query.Season, query.Episode); len(paths) > 0 {
		return paths, nil
	}

	return nil, ErrNoSubtitle

}

func pickSubDLDownload(response subdlSearchResponse, season, episode int) string {

	paths := pickSubDLPaths(response, season, episode)

	if len(paths) == 0 {
		return ""
	}

	return paths[0]

}

func pickSubDLPaths(response subdlSearchResponse, season, episode int) []string {

	if season > 0 && episode > 0 {
		return pickSubDLEpisodePaths(response, season, episode)
	}

	var paths []string

	for _, subtitle := range response.Subtitles {

		for _, file := range subtitle.UnpackFiles {

			if !looksEnglishLanguageTag(file.Language) || hasForeignLanguageName(file.Name) {
				continue
			}

			if strings.EqualFold(strings.TrimSpace(file.Format), "srt") || strings.HasSuffix(strings.ToLower(file.Name), ".srt") {
				if path := strings.TrimSpace(file.URL); path != "" {
					paths = append(paths, path)
				}
			}

		}

		if path := strings.TrimSpace(subtitle.URL); path != "" {
			paths = append(paths, path)
		}

	}

	return paths

}

func pickSubDLEpisodePaths(response subdlSearchResponse, season, episode int) []string {

	var paths []string

	// Season-pack unpack files are the most reliable source; match by filename first.
	for _, subtitle := range response.Subtitles {

		for _, file := range subtitle.UnpackFiles {

			if !fileMatchesEpisode(file, season, episode) {
				continue
			}

			if path := strings.TrimSpace(file.URL); path != "" {
				paths = append(paths, path)
			}

		}

	}

	// Single-episode releases with one unpacked file are the next best option.
	for _, subtitle := range response.Subtitles {

		if !subtitleMatchesEpisode(subtitle, season, episode) {
			continue
		}

		if len(subtitle.UnpackFiles) == 1 {
			file := subtitle.UnpackFiles[0]
			if looksEnglishLanguageTag(file.Language) && !hasForeignLanguageName(file.Name) {
				if path := strings.TrimSpace(file.URL); path != "" {
					paths = append(paths, path)
				}
			}
		}

	}

	// Direct file URLs when SubDL unpacks a single episode.
	for _, subtitle := range response.Subtitles {

		if !subtitleMatchesEpisode(subtitle, season, episode) {
			continue
		}

		if len(subtitle.UnpackFiles) == 0 {

			path := strings.TrimSpace(subtitle.URL)

			if path != "" && !strings.HasSuffix(strings.ToLower(path), ".zip") {
				paths = append(paths, path)
			}

		}

	}

	// Season-pack archives when SubDL omits unpack_files (common for some titles).
	paths = append(paths, pickSubDLSeasonZipPaths(response, season)...)

	return paths

}

func pickSubDLSeasonZipPaths(response subdlSearchResponse, season int) []string {

	var preferred []string
	var fallback []string

	for _, subtitle := range response.Subtitles {

		if !seasonMatches(subtitle.Season, season) {
			continue
		}

		path := strings.TrimSpace(subtitle.URL)

		if path == "" || !strings.HasSuffix(strings.ToLower(path), ".zip") {
			continue
		}

		joined := strings.ToLower(subtitle.ReleaseName + " " + subtitle.Name)

		if strings.Contains(joined, "forced") {
			fallback = append(fallback, path)
			continue
		}

		preferred = append(preferred, path)

	}

	return append(preferred, fallback...)

}

func nameMatchesEpisode(name string, season, episode int) bool {

	if s, e := parseEpisodeTag(name); e == episode && seasonMatches(s, season) {
		return true
	}

	if e := parseLeadingEpisode(name); e == episode {
		return true
	}

	return false

}

func subtitleMatchesEpisode(subtitle subdlSubtitle, season, episode int) bool {

	if subtitle.Episode == episode && seasonMatches(subtitle.Season, season) {
		return true
	}

	for _, label := range []string{subtitle.ReleaseName, subtitle.Name} {

		if s, e := parseEpisodeTag(label); e == episode && seasonMatches(s, season) {
			return true
		}

	}

	return false

}

func fileMatchesEpisode(file subdlUnpackFile, season, episode int) bool {

	if !looksEnglishLanguageTag(file.Language) || hasForeignLanguageName(file.Name) {
		return false
	}

	for _, label := range []string{file.Name, file.ReleaseName} {

		if s, e := parseEpisodeTag(label); e == episode && seasonMatches(s, season) {
			return true
		}

		if e := parseLeadingEpisode(label); e == episode {
			return true
		}

	}

	if file.Episode == episode && seasonMatches(file.Season, season) {
		return true
	}

	return false

}

func seasonMatches(got, want int) bool {

	return got == 0 || got == want

}

func parseEpisodeTag(label string) (season, episode int) {

	label = strings.TrimSpace(label)

	if label == "" {
		return 0, 0
	}

	if match := episodeTagRE.FindStringSubmatch(label); len(match) == 3 {
		season, _ = strconv.Atoi(match[1])
		episode, _ = strconv.Atoi(match[2])
		return season, episode
	}

	if match := episodeXRE.FindStringSubmatch(label); len(match) == 3 {
		season, _ = strconv.Atoi(match[1])
		episode, _ = strconv.Atoi(match[2])
		return season, episode
	}

	return 0, 0

}

func parseLeadingEpisode(label string) int {

	label = strings.TrimSpace(label)

	if label == "" {
		return 0
	}

	base := label
	if idx := strings.Index(label, "/"); idx >= 0 {
		base = label[idx+1:]
	}

	match := leadingEpisodeRE.FindStringSubmatch(base)

	if len(match) != 2 {
		return 0
	}

	episode, err := strconv.Atoi(match[1])

	if err != nil {
		return 0
	}

	return episode

}

func (c *SubDLClient) downloadBytes(ctx context.Context, path string) ([]byte, error) {

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Free API keys search with api_key but download anonymously; api_key on dl.subdl.com requires a paid plan.
	downloadURL := subDLDownload + path

	resp, err := c.http.R().SetContext(ctx).Get(downloadURL)

	if err != nil {
		return nil, err
	}

	if resp.IsErrorState() {
		return nil, mapSubDLError(resp.StatusCode, resp.String())
	}

	return resp.Bytes(), nil

}

func imdbQueryID(id string) string {

	id = strings.TrimSpace(id)

	if id == "" {
		return ""
	}

	if !strings.HasPrefix(strings.ToLower(id), "tt") {
		return "tt" + id
	}

	return id

}

func mapSubDLError(status int, body string) error {

	switch status {
	case 401, 403:
		return fmt.Errorf("%w: %s", ErrUnauthorized, strings.TrimSpace(body))
	case 429:
		return fmt.Errorf("%w: %s", ErrRateLimited, strings.TrimSpace(body))
	case 404:
		return ErrNoSubtitle
	default:
		return fmt.Errorf("captions: subdl request failed with status %d", status)
	}

}

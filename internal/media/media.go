package media

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"streamly/internal/config"
	"streamly/internal/febapi"
)

const movie = febapi.BoxMovie // Showbox's discriminator for a movie.

// Selection is a title resolved from a search hit or an autocomplete selection.
type Selection struct {
	ID      int
	BoxType febapi.BoxType
}

// TitleDetails is user-facing metadata for a title.
type TitleDetails struct {
	Title         string
	Year          string
	Poster        string
	Description   string
	IMDBRating    string
	EpisodeTitles map[string]string
}

// Resolver bridges Showbox search and Febbox browsing into what the bot needs to stream.
type Resolver struct {
	showbox *febapi.ShowboxClient
	febbox  *febapi.FebboxClient
}

func NewResolver() *Resolver {

	return &Resolver{
		showbox: febapi.NewShowboxClient(febapi.ShowboxOptions{}),
		febbox:  febapi.NewFebboxClient(febapi.FebboxOptions{Cookie: config.App.FebboxCookie}),
	}

}

func (r *Resolver) Search(query string) ([]febapi.SearchResult, error) {

	results, err := r.showbox.Search(query, febapi.MediaAll, 1, 25)

	if err != nil {
		return nil, err
	}

	return results, nil

}

func (r *Resolver) ResolveSelection(value string) (*Selection, error) {

	if encoded := regexp.MustCompile(`^([12]):(\d+)$`).FindStringSubmatch(value); len(encoded) == 3 {

		boxType, _ := strconv.Atoi(encoded[1])
		id, _ := strconv.Atoi(encoded[2])

		return &Selection{ID: id, BoxType: febapi.BoxType(boxType)}, nil

	}

	hits, err := r.showbox.Search(value, febapi.MediaAll, 1, 1)

	if err != nil {
		return nil, err
	}

	if len(hits) == 0 {
		return nil, nil
	}

	return &Selection{ID: hits[0].ID, BoxType: hits[0].BoxType}, nil

}

func (r *Resolver) Details(selection Selection) (TitleDetails, error) {

	var raw map[string]any
	var err error

	if r.IsMovie(selection) {
		raw, err = r.showbox.GetMovie(selection.ID)
	} else {
		raw, err = r.showbox.GetShow(selection.ID)
	}

	if err != nil {
		return TitleDetails{}, err
	}

	text := func(key string) string {
		value, ok := raw[key]
		if !ok || value == nil || value == "" {
			return ""
		}
		return fmt.Sprint(value)
	}

	return TitleDetails{
		Title:         fallback(text("title"), "Unknown title"),
		Year:          text("year"),
		Poster:        fallback(text("poster"), fallback(text("poster_org"), text("poster_min"))),
		Description:   text("description"),
		IMDBRating:    text("imdb_rating"),
		EpisodeTitles: episodeTitleMap(raw),
	}, nil

}

func (r *Resolver) ShareKey(selection Selection) (string, error) {

	return r.showbox.GetFebBoxID(selection.ID, selection.BoxType)

}

func (r *Resolver) ListChildren(shareKey string, parentID int) ([]febapi.FebboxFile, error) {

	return r.febbox.ListFiles(shareKey, parentID, "")

}

func (r *Resolver) Files(entries []febapi.FebboxFile) []febapi.FebboxFile {

	var files []febapi.FebboxFile

	for _, entry := range entries {
		if entry.IsDir == 0 {
			files = append(files, entry)
		}
	}

	sort.Slice(files, func(i, j int) bool { return byName(files[i], files[j]) })

	return files

}

func (r *Resolver) Seasons(entries []febapi.FebboxFile) []febapi.FebboxFile {

	var seasons []febapi.FebboxFile

	for _, entry := range entries {
		if entry.IsDir == 1 {
			seasons = append(seasons, entry)
		}
	}

	sort.Slice(seasons, func(i, j int) bool { return byName(seasons[i], seasons[j]) })

	return seasons

}

func (r *Resolver) IsMovie(selection Selection) bool {

	return selection.BoxType == movie

}

func (r *Resolver) MovieFile(shareKey string) (*febapi.FebboxFile, error) {

	root, err := r.ListChildren(shareKey, 0)

	if err != nil {
		return nil, err
	}

	direct := r.Files(root)

	if len(direct) > 0 {
		return &direct[0], nil
	}

	seasons := r.Seasons(root)

	if len(seasons) == 0 {
		return nil, nil
	}

	children, err := r.ListChildren(shareKey, seasons[0].FID)

	if err != nil {
		return nil, err
	}

	files := r.Files(children)

	if len(files) == 0 {
		return nil, nil
	}

	return &files[0], nil

}

func (r *Resolver) StreamURL(shareKey string, fid int, target int) (string, error) {

	qualities, err := r.febbox.GetLinks(shareKey, fid, "")

	if err != nil {
		return "", err
	}

	picked := PickQuality(qualities, target)

	if picked == nil {
		return "", nil
	}

	return picked.URL, nil

}

func (r *Resolver) Qualities(shareKey string, fid int) ([]febapi.FileQuality, error) {

	qualities, err := r.febbox.GetLinks(shareKey, fid, "")

	if err != nil {
		return nil, err
	}

	sort.Slice(qualities, func(i, j int) bool { return QualityHeight(qualities[i]) < QualityHeight(qualities[j]) })

	return qualities, nil

}

func QualityHeight(quality febapi.FileQuality) int {

	label := quality.Quality + " " + quality.Name

	if regexp.MustCompile(`(?i)2160|4k`).MatchString(label) {
		return 2160
	}

	if match := regexp.MustCompile(`(\d{3,4})\s*p`).FindStringSubmatch(label); len(match) > 1 {
		height, _ := strconv.Atoi(match[1])
		return height
	}

	if regexp.MustCompile(`(?i)org|origin`).MatchString(label) {
		return math.MaxInt
	}

	return 0

}

func PickQuality(qualities []febapi.FileQuality, target int) *febapi.FileQuality {

	if len(qualities) == 0 {
		return nil
	}

	sorted := append([]febapi.FileQuality(nil), qualities...)
	sort.Slice(sorted, func(i, j int) bool { return QualityHeight(sorted[i]) < QualityHeight(sorted[j]) })

	for i := range sorted {
		if QualityHeight(sorted[i]) >= target {
			return &sorted[i]
		}
	}

	return &sorted[len(sorted)-1]

}

func byName(a, b febapi.FebboxFile) bool {

	return strings.Compare(a.FileName, b.FileName) < 0

}

func episodeTitleMap(raw map[string]any) map[string]string {

	episodes, ok := raw["episode"].([]any)

	if !ok {
		return nil
	}

	titles := make(map[string]string)

	for _, item := range episodes {

		data, ok := item.(map[string]any)

		if !ok {
			continue
		}

		season, _ := data["season"].(float64)
		number, _ := data["episode"].(float64)
		title := strings.TrimSpace(fmt.Sprint(data["title"]))

		if season > 0 && number > 0 && title != "" {
			titles[fmt.Sprintf("%d:%d", int(season), int(number))] = title
		}

	}

	if len(titles) == 0 {
		return nil
	}

	return titles

}

func fallback(values ...string) string {

	for _, value := range values {
		if value != "" {
			return value
		}
	}

	return ""

}
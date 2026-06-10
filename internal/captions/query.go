package captions

import "fmt"

// Query identifies a title for subtitle lookup.
type Query struct {
	IMDBId    string
	TMDBId    int
	ShareKey  string
	VideoFID  int
	VideoName string
	Season    int
	Episode   int
}

// QueryKey fingerprints a query so cached subtitle files can be reused safely.
func QueryKey(query Query) string {

	return fmt.Sprintf("%s|%d|%d|%d|%d", query.IMDBId, query.TMDBId, query.Season, query.Episode, query.VideoFID)

}
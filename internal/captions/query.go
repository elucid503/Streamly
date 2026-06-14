package captions

import "fmt"

type Query struct {

	IMDBId string
	TMDBId int

	ShareKey string

	VideoFID int
	VideoName string

	Season int
	Episode int

}

func QueryKey(query Query) string {

	return fmt.Sprintf("%s|%d|%d|%d|%d", query.IMDBId, query.TMDBId, query.Season, query.Episode, query.VideoFID)

}

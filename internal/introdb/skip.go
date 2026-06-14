package introdb

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const minIntroDurationMs = 15_000

func IntroSkipTarget(record *MediaRecord, position time.Duration) (time.Duration, error) {

	if record == nil || len(record.Intro) == 0 {

		return 0, ErrNoIntroData
	}

	positionMs := position.Milliseconds()
	viable := viableIntros(record.Intro)

	if len(viable) == 0 {

		return 0, ErrNoIntroData
	}

	for _, segment := range viable {

		endMs := *segment.EndMs

		if positionMs >= segment.StartMs && positionMs < endMs {

			return time.Duration(endMs) * time.Millisecond, nil

		}

	}

	intro := viable[0]

	endMs := *intro.EndMs

	if positionMs < endMs {

		return time.Duration(endMs) * time.Millisecond, nil
	}

	for _, segment := range viable[1:] {

		if positionMs < *segment.EndMs {

			return 0, ErrNotInIntro
		}

	}

	return 0, ErrPastIntro

}

func viableIntros(segments []SegmentTimestamp) []SegmentTimestamp {

	var viable []SegmentTimestamp

	for _, segment := range segments {

		if segment.EndMs != nil {

			viable = append(viable, segment)

		}

	}

	if len(viable) == 0 {

		return nil
	}

	sort.Slice(viable, func(i, j int) bool {

		return viable[i].StartMs < viable[j].StartMs

	})

	for len(viable) > 1 && introDuration(viable[0]) < minIntroDurationMs {

		viable = viable[1:]

	}

	return viable

}

func introDuration(segment SegmentTimestamp) int64 {

	return *segment.EndMs - segment.StartMs
}

func QueryForTitle(tmdbID int, imdbID string, season, episode int, durationMs *int64) (MediaQuery, error) {

	if tmdbID <= 0 && strings.TrimSpace(imdbID) == "" {

		return MediaQuery{}, fmt.Errorf("no external id for intro lookup")

	}

	query := MediaQuery{

		TMDBId: tmdbID,
		IMDBId: strings.TrimSpace(imdbID),

		Season: season,
		Episode: episode,

	}

	if durationMs != nil {

		query.DurationMs = *durationMs

	}

	return query, nil

}

func MapGetMediaError(err error) error {

	if err == nil {

		return nil

	}

	if errors.Is(err, ErrNotFound) {

		return ErrNoIntroData

	}

	return err

}

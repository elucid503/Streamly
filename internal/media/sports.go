package media

import (
	"fmt"
	"strings"

	"streamly/internal/tvapi"
)

const sportsSelectionPrefix = "sport:"

type SportsSelection struct {

	DaddyID string
	Title string
	League string

}

func (r *Resolver) SportsSearch(query string, limit int) ([]tvapi.SportsEvent, error) {

	events, err := r.tv.Sports()

	if err != nil {

		return nil, err

	}

	if limit <= 0 {

		limit = 25

	}

	query = strings.ToLower(strings.TrimSpace(query))

	if query == "" {

		if len(events) > limit {

			return events[:limit], nil

		}

		return events, nil

	}

	matches := make([]tvapi.SportsEvent, 0, limit)

	for _, event := range events {

		haystack := strings.ToLower(event.League + " " + event.Title)

		if strings.Contains(haystack, query) {

			matches = append(matches, event)

		}

		if len(matches) >= limit {

			break

		}

	}

	return matches, nil

}

// ResolveSportsSelection turns an autocomplete value or free text into a streamable selection.
func (r *Resolver) ResolveSportsSelection(value string) (*SportsSelection, error) {

	value = strings.TrimSpace(value)

	if value == "" {

		return nil, nil

	}

	if strings.HasPrefix(value, sportsSelectionPrefix) {

		payload := strings.TrimPrefix(value, sportsSelectionPrefix)
		daddyID, title, _ := strings.Cut(payload, "|")

		daddyID = strings.TrimSpace(daddyID)

		if daddyID == "" {

			return nil, nil

		}

		return &SportsSelection{DaddyID: daddyID, Title: strings.TrimSpace(title)}, nil

	}

	matches, err := r.SportsSearch(value, 1)

	if err != nil {

		return nil, err

	}

	if len(matches) == 0 || len(matches[0].Channels) == 0 {

		return nil, nil

	}

	event := matches[0]

	return &SportsSelection{

		DaddyID: event.Channels[0].DaddyID,
		Title: event.Title,
		League: event.League,

	}, nil

}

func SportsSelectionValue(event tvapi.SportsEvent) string {

	if len(event.Channels) == 0 {

		return ""

	}

	value := sportsSelectionPrefix + event.Channels[0].DaddyID + "|" + event.Title

	if len(value) > 100 {

		value = value[:100]

	}

	return value

}

func SportsLabel(event tvapi.SportsEvent) string {

	if event.League != "" {

		return fmt.Sprintf("%s • %s", event.League, event.Title)

	}

	return event.Title

}

func SportsChannel(selection SportsSelection) tvapi.Channel {

	name := selection.Title

	if name == "" {

		name = "Live Sports"

	}

	return tvapi.Channel{

		DaddyID: selection.DaddyID,
		Name: name,
		Category: fallback(selection.League, "Sports"),

	}

}

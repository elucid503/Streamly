package media

import (
	"fmt"
	"regexp"
	"strings"

	"streamly/internal/tvapi"
)

const tvSelectionPrefix = "tv:"

// TVSelection is a live TV channel picked from autocomplete or typed directly.
type TVSelection struct {

	DaddyID string
	Channel tvapi.Channel

}

// SearchTV returns catalog channels matching query. An empty query yields popular US channels.
func (r *Resolver) SearchTV(query string, limit int) ([]tvapi.Channel, error) {

	catalog, err := r.tv.ListChannels()

	if err != nil {

		return nil, err

	}

	if limit <= 0 {

		limit = 25

	}

	query = strings.TrimSpace(query)

	if query == "" {

		return catalog.PopularUS(limit), nil

	}

	return catalog.Search(query, limit), nil

}

// ResolveTVSelection parses an autocomplete value or free-typed channel name into a TVSelection.
func (r *Resolver) ResolveTVSelection(value string) (*TVSelection, error) {

	value = strings.TrimSpace(value)

	if value == "" {

		return nil, nil

	}

	if match := regexp.MustCompile(`^tv:(\d+)$`).FindStringSubmatch(value); len(match) == 2 {

		catalog, err := r.tv.ListChannels()

		if err != nil {

			return nil, err

		}

		channel, ok := catalog.FindByID(match[1])

		if !ok {
			return nil, fmt.Errorf("channel not found: daddyId %s", match[1])
		}

		return &TVSelection{DaddyID: channel.DaddyID, Channel: channel}, nil

	}

	catalog, err := r.tv.ListChannels()

	if err != nil {

		return nil, err

	}

	if channel, ok := catalog.FindByName(value); ok {

		return &TVSelection{DaddyID: channel.DaddyID, Channel: channel}, nil

	}

	if channel, ok := catalog.FindBySlug(value); ok {

		return &TVSelection{DaddyID: channel.DaddyID, Channel: channel}, nil

	}

	hits := catalog.Search(value, 1)

	if len(hits) == 0 {

		return nil, nil

	}

	channel := hits[0]

	return &TVSelection{DaddyID: channel.DaddyID, Channel: channel}, nil

}

// TVSelectionValue encodes a channel for Discord autocomplete.
func TVSelectionValue(daddyID string) string {

	return tvSelectionPrefix + daddyID

}

// TVAutocompleteLabel formats a channel for the /stream autocomplete menu.
func TVAutocompleteLabel(channel tvapi.Channel) string {

	return fmt.Sprintf("Live TV • %s", channel.Name)

}

// TVStreamURL resolves a fresh HLS playlist URL for a live channel.
func (r *Resolver) TVStreamURL(daddyID string) (string, error) {

	return r.tv.ResolveHLS(daddyID)

}

// TVDetails maps a live channel into the shared TitleDetails embed shape.
func TVDetails(channel tvapi.Channel) TitleDetails {

	return TitleDetails{

		Title:  channel.Name,
		Poster: channel.Logo,

	}

}

package media

import (
	"fmt"
	"regexp"
	"strings"

	"streamly/internal/tvapi"
)

const tvSelectionPrefix = "tv:"

type TVSelection struct {

	DaddyID string
	Channel tvapi.Channel

}

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

		return &TVSelection{

			DaddyID: channel.DaddyID,
			Channel: channel,

		}, nil

	}

	catalog, err := r.tv.ListChannels()

	if err != nil {

		return nil, err

	}

	if channel, ok := catalog.FindByName(value); ok {

		return &TVSelection{

			DaddyID: channel.DaddyID,
			Channel: channel,

		}, nil

	}

	if channel, ok := catalog.FindBySlug(value); ok {

		return &TVSelection{

			DaddyID: channel.DaddyID,
			Channel: channel,

		}, nil

	}

	hits := catalog.Search(value, 1)

	if len(hits) == 0 {

		return nil, nil

	}

	channel := hits[0]

	return &TVSelection{

		DaddyID: channel.DaddyID,
		Channel: channel,

	}, nil

}

func TVSelectionValue(daddyID string) string {

	return tvSelectionPrefix + daddyID

}

func TVAutocompleteLabel(channel tvapi.Channel) string {

	return fmt.Sprintf("Live TV • %s", channel.Name)

}

func (r *Resolver) TVStreamURL(daddyID string) (string, error) {

	stream, err := r.tv.ResolveStream(daddyID)

	if err != nil {

		return "", err
	}

	return stream.URL, nil

}

func (r *Resolver) TVStreamEndpoint(daddyID string) (tvapi.ResolvedStream, error) {

	return r.tv.ResolveStream(daddyID)

}

func TVDetails(channel tvapi.Channel) TitleDetails {

	return TitleDetails{

		Title: channel.Name,
		Poster: channel.Logo,

	}

}

package media

import (
	"fmt"
	"regexp"
	"strings"

	"streamly/internal/tvapi"
)

const tvSelectionPrefix = "tv:"

type TVSelection struct {
	ChannelID string
	Channel   tvapi.Channel
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

	if match := regexp.MustCompile(`^tv:(.+)$`).FindStringSubmatch(value); len(match) == 2 {

		catalog, err := r.tv.ListChannels()

		if err != nil {

			return nil, err

		}

		channel, ok := catalog.FindByID(match[1])

		if !ok {

			return nil, fmt.Errorf("channel not found: %s", match[1])
		}

		return &TVSelection{

			ChannelID: channel.ID,
			Channel:   channel,
		}, nil

	}

	catalog, err := r.tv.ListChannels()

	if err != nil {

		return nil, err

	}

	if channel, ok := catalog.FindByName(value); ok {

		return &TVSelection{

			ChannelID: channel.ID,
			Channel:   channel,
		}, nil

	}

	if channel, ok := catalog.FindBySlug(value); ok {

		return &TVSelection{

			ChannelID: channel.ID,
			Channel:   channel,
		}, nil

	}

	hits := catalog.Search(value, 1)

	if len(hits) == 0 {

		return nil, nil

	}

	channel := hits[0]

	return &TVSelection{

		ChannelID: channel.ID,
		Channel:   channel,
	}, nil

}

func TVSelectionValue(channelID string) string {

	return tvSelectionPrefix + channelID

}

func TVAutocompleteLabel(channel tvapi.Channel) string {

	return fmt.Sprintf("Live TV • %s", channel.Name)

}

// TVAutocompleteLabelWithShow prefers "Live TV • Show on Channel" when a current
// show is known, appending the upcoming show when available, and falls back to
// the plain channel label otherwise.
func (r *Resolver) TVAutocompleteLabelWithShow(channel tvapi.Channel) string {

	now, next := r.TVNowNext(channel)

	switch {

	case now != "" && next != "":

		return fmt.Sprintf("Live TV • %s on %s · Next: %s", now, channel.Name, next)

	case now != "":

		return fmt.Sprintf("Live TV • %s on %s", now, channel.Name)

	case next != "":

		return fmt.Sprintf("Live TV • %s · Next: %s", channel.Name, next)

	default:

		return TVAutocompleteLabel(channel)

	}

}

// TVNowNext returns the currently airing show and the next show on the channel.
func (r *Resolver) TVNowNext(channel tvapi.Channel) (string, string) {

	now, next := r.tvmaze.NowNext(channel.Name)

	nowShow := ""
	nextShow := ""

	if now != nil {

		nowShow = now.Show

	}

	if next != nil {

		nextShow = next.Show

	}

	return nowShow, nextShow

}

func (r *Resolver) TVStreamURL(channelID string) (string, error) {

	stream, err := r.tv.ResolveStream(channelID)

	if err != nil {

		return "", err
	}

	return stream.URL, nil

}

func (r *Resolver) TVStreamEndpoint(channelID string) (tvapi.ResolvedStream, error) {

	return r.tv.ResolveStream(channelID)

}

func TVDetails(channel tvapi.Channel) TitleDetails {

	return TitleDetails{

		Title:  channel.Name,
		Poster: channel.Logo,
	}

}

func (r *Resolver) TVChannelThumb(logoURL string) ([]byte, error) {

	return r.tv.ChannelThumb(logoURL)

}

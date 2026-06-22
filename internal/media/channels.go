package media

import (
	"sort"

	"streamly/internal/tvapi"
)

const ChannelPageSize = 12

type GuidedChannel struct {
	Channel tvapi.Channel

	Now  string
	Next string
}

func (r *Resolver) ListTVChannels(page int) ([]tvapi.Channel, int, error) {

	if page < 1 {

		page = 1

	}

	catalog, err := r.tv.ListChannels()

	if err != nil {

		return nil, 0, err

	}

	channels := catalog.Sorted()
	totalPages := pageCount(len(channels), ChannelPageSize)

	start := (page - 1) * ChannelPageSize

	if start >= len(channels) {

		return nil, totalPages, nil

	}

	end := start + ChannelPageSize

	if end > len(channels) {

		end = len(channels)

	}

	return channels[start:end], totalPages, nil

}

// ListTVChannelsGuided returns a page of channels annotated with their current
// and next show. Channels currently airing a show are surfaced first.
func (r *Resolver) ListTVChannelsGuided(page int) ([]GuidedChannel, int, error) {

	if page < 1 {

		page = 1

	}

	catalog, err := r.tv.ListChannels()

	if err != nil {

		return nil, 0, err

	}

	channels := catalog.Sorted()

	guided := make([]GuidedChannel, 0, len(channels))

	for _, channel := range channels {

		now, next := r.TVNowNext(channel)

		guided = append(guided, GuidedChannel{Channel: channel, Now: now, Next: next})

	}

	sort.SliceStable(guided, func(i, j int) bool {

		return guided[i].Now != "" && guided[j].Now == ""

	})

	totalPages := pageCount(len(guided), ChannelPageSize)

	start := (page - 1) * ChannelPageSize

	if start >= len(guided) {

		return nil, totalPages, nil

	}

	end := start + ChannelPageSize

	if end > len(guided) {

		end = len(guided)

	}

	return guided[start:end], totalPages, nil

}

func pageCount(total, pageSize int) int {

	if total == 0 {

		return 1

	}

	pages := total / pageSize

	if total%pageSize != 0 {

		pages++

	}

	if pages < 1 {

		return 1
	}

	return pages

}

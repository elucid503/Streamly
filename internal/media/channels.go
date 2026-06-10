package media

import (
	"streamly/internal/tvapi"
)

const ChannelPageSize = 10

// ListTVChannels returns a page of live TV channels from the catalog.
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
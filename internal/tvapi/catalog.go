package tvapi

import (
	"sort"
	"strings"
)

// popularUSSlugs is a curated popularity order for United States channels.
var popularUSSlugs = []string{
	"espn-usa",
	"cnn-usa",
	"abc-usa",
	"cbs-usa",
	"nbc-usa",
	"fox-usa",
	"fox-sports-1-usa",
	"discovery-channel",
	"comedy-central",
	"hbo-usa",
	"espn2-usa",
	"tnt-usa",
	"usa-network",
	"fx-usa",
	"mtv-usa",
	"disney-channel",
	"cartoon-network",
	"national-geographic",
	"cnbc-usa",
	"bravo-usa",
}

// FindByID returns the channel with the given id, if present.
func (catalog *ChannelCatalog) FindByID(ID string) (Channel, bool) {

	for _, channel := range catalog.Channels {

		if channel.DaddyID == ID {

			return channel, true

		}
	}

	return Channel{}, false

}

// FindBySlug returns the channel with the given slug, if present.
func (catalog *ChannelCatalog) FindBySlug(slug string) (Channel, bool) {

	slug = strings.ToLower(strings.TrimSpace(slug))

	for _, channel := range catalog.Channels {

		if strings.ToLower(channel.Slug) == slug {

			return channel, true

		}
	}

	return Channel{}, false

}

// FindByName returns the first channel whose name matches case-insensitively.
func (catalog *ChannelCatalog) FindByName(name string) (Channel, bool) {

	name = strings.ToLower(strings.TrimSpace(name))

	for _, channel := range catalog.Channels {

		if strings.ToLower(channel.Name) == name {

			return channel, true

		}
	}

	return Channel{}, false

}

// Filter returns channels matching optional country code and/or category (case-insensitive).
func (catalog *ChannelCatalog) Filter(countryCode, category string) []Channel {

	countryCode = strings.ToLower(strings.TrimSpace(countryCode))
	category = strings.ToLower(strings.TrimSpace(category))

	var matches []Channel

	for _, channel := range catalog.Channels {

		if countryCode != "" && strings.ToLower(channel.Country.Code) != countryCode {

			continue

		}

		if category != "" && strings.ToLower(channel.Category) != category {

			continue

		}

		matches = append(matches, channel)

	}

	return matches

}

// Search returns channels whose name or slug contains query (case-insensitive).
func (catalog *ChannelCatalog) Search(query string, limit int) []Channel {

	query = strings.ToLower(strings.TrimSpace(query))

	if query == "" {
		return nil
	}

	var matches []Channel

	for _, channel := range catalog.Channels {

		name := strings.ToLower(channel.Name)
		slug := strings.ToLower(channel.Slug)

		if strings.Contains(name, query) || strings.Contains(slug, query) {

			matches = append(matches, channel)

		}

	}

	sort.Slice(matches, func(i, j int) bool {

		return strings.Compare(matches[i].Name, matches[j].Name) < 0

	})

	if limit > 0 && len(matches) > limit {

		matches = matches[:limit]

	}

	return matches

}

// PopularUS returns up to limit United States channels ranked by curated popularity.
func (catalog *ChannelCatalog) PopularUS(limit int) []Channel {

	if limit <= 0 {

		limit = 5

	}

	us := catalog.Filter("us", "")

	sort.Slice(us, func(i, j int) bool {

		left := popularityRank(us[i].Slug)
		right := popularityRank(us[j].Slug)

		if left != right {

			return left < right

		}

		return strings.Compare(us[i].Name, us[j].Name) < 0

	})

	if len(us) > limit {

		us = us[:limit]

	}

	return us

}

// Sorted returns channels ranked by US popularity, then alphabetically by name.
func (catalog *ChannelCatalog) Sorted() []Channel {

	channels := append([]Channel(nil), catalog.Channels...)

	sort.Slice(channels, func(i, j int) bool {

		left := popularityRank(channels[i].Slug)
		right := popularityRank(channels[j].Slug)

		if left != right {
			return left < right
		}

		return strings.Compare(channels[i].Name, channels[j].Name) < 0

	})

	return channels

}

func popularityRank(slug string) int {

	slug = strings.ToLower(slug)

	for index, popular := range popularUSSlugs {

		if popular == slug {

			return index

		}
	}

	return len(popularUSSlugs)

}

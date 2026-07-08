package tvapi

import (
	"sort"
	"strings"
)

// knownSportsSlugs are catalog channels commonly streamed for live games.
var knownSportsSlugs = map[string]struct{}{

	"espn": {},
	"espn-2": {},
	"espn-usa": {},
	"espn-news": {},
	"espn-u": {},
	"espn-deportes": {},
	"fox-sports-1": {},
	"fox-sports-2": {},
	"nfl-network": {},
	"nfl-redzone": {},
	"nba-tv": {},
	"mlb-network": {},
	"nhl-network": {},
	"cbs-sports-network": {},
	"golf-channel": {},
	"bein-sports": {},
	"tennis-channel": {},
	"tnt-sports-1": {},
	"tnt-sports-2": {},
	"tnt-sports-3": {},
	"tnt-sports-4": {},
	"sky-sports-main-event": {},
	"sky-sports-football": {},
	"sky-sports-premier-league": {},
	"sky-sports-f1": {},
	"sky-sports-cricket": {},
	"dazn-1": {},
	"sportsnet-one": {},
	"willow": {},
	"star-sports": {},

}

func IsSportsChannel(channel Channel) bool {

	if strings.EqualFold(strings.TrimSpace(channel.Category), "Sports") {

		return true

	}

	_, ok := knownSportsSlugs[strings.ToLower(strings.TrimSpace(channel.Slug))]

	return ok

}

var popularUSSlugs = []string{

	"espn-usa",
	"espn",
	"cnn",
	"abc",
	"cbs",
	"nbc",
	"fox",
	"fox-sports-1",
	"discovery-channel",
	"comedy-central",
	"hbo",
	"espn-2",
	"tnt",
	"usa-network",
	"fx",
	"mtv",
	"disney-channel",
	"cartoon-network",
	"national-geographic",
	"cnbc",
	"bravo",

}

func (catalog *ChannelCatalog) FindByID(ID string) (Channel, bool) {

	ID = strings.TrimSpace(ID)

	for _, channel := range catalog.Channels {

		if channel.ID == ID {

			return channel, true

		}

	}

	return Channel{}, false

}

func (catalog *ChannelCatalog) FindBySlug(slug string) (Channel, bool) {

	slug = strings.ToLower(strings.TrimSpace(slug))

	for _, channel := range catalog.Channels {

		if strings.ToLower(channel.Slug) == slug {

			return channel, true

		}

	}

	return Channel{}, false

}

func (catalog *ChannelCatalog) FindByName(name string) (Channel, bool) {

	name = strings.ToLower(strings.TrimSpace(name))

	for _, channel := range catalog.Channels {

		if strings.ToLower(channel.Name) == name {

			return channel, true

		}

	}

	return Channel{}, false

}

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

func (catalog *ChannelCatalog) PopularUS(limit int) []Channel {

	if limit <= 0 {

		limit = 5

	}

	us := catalog.Filter("us", "")

	// Scraped catalogs carry no country metadata, so the country filter comes back
	// empty; fall back to the global popularity ranking, which still surfaces the
	// popular US channels first via their slugs.
	if len(us) == 0 {

		us = catalog.Sorted()

	} else {

		sort.Slice(us, func(i, j int) bool {

			left := popularityRank(us[i].Slug)

			right := popularityRank(us[j].Slug)

			if left != right {

				return left < right

			}

			return strings.Compare(us[i].Name, us[j].Name) < 0

		})

	}

	if len(us) > limit {

		us = us[:limit]

	}

	return us

}

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

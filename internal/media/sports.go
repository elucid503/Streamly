package media

import (
	"fmt"
	"strings"
	"time"

	"streamly/internal/tvapi"
)

const sportsSelectionPrefix = "sport:"

type SportsSelection struct {
	ChannelID string
	Title     string
	League    string

	HomeTeam string
	AwayTeam string
	MatchID  string
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

		haystack := strings.ToLower(event.League + " " + event.Title + " " + event.HomeTeam + " " + event.AwayTeam)

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
		channelID, rest, _ := strings.Cut(payload, "|")
		title, matchID, _ := strings.Cut(rest, "|")

		channelID = strings.TrimSpace(channelID)

		if channelID == "" {

			return nil, nil

		}

		return &SportsSelection{

			ChannelID: channelID,
			Title:     strings.TrimSpace(title),
			MatchID:   strings.TrimSpace(matchID),
		}, nil

	}

	matches, err := r.SportsSearch(value, 1)

	if err != nil {

		return nil, err

	}

	if len(matches) == 0 {

		return nil, nil

	}

	event := matches[0]
	channel, ok := r.tv.ResolveMatchChannel(event)

	if !ok {

		return nil, nil

	}

	return &SportsSelection{

		ChannelID: channel.ID,
		Title:     event.Title,
		League:    event.League,

		HomeTeam: event.HomeTeam,
		AwayTeam: event.AwayTeam,
		MatchID:  event.ID,
	}, nil

}

func SportsSelectionValue(event tvapi.SportsEvent) string {

	channelID := ""

	if len(event.Channels) > 0 {

		channelID = event.Channels[0].ChannelID

	}

	if channelID == "" {

		// Selection value still encodes the match so resolve can scrape broadcasters.
		channelID = "match"

	}

	value := sportsSelectionPrefix + channelID + "|" + event.Title + "|" + event.ID

	if len(value) > 100 {

		// Prefer keeping the match id for resolve; trim the title.
		suffix := "|" + event.ID
		prefix := sportsSelectionPrefix + channelID + "|"
		budget := 100 - len(prefix) - len(suffix)

		if budget < 8 {

			value = value[:100]

		} else {

			title := event.Title

			if len(title) > budget {

				title = title[:budget]

			}

			value = prefix + title + suffix

		}

	}

	return value

}

// ResolveSportsSelectionChannel resolves the 24/7 channel for a sports selection.
func (r *Resolver) ResolveSportsSelectionChannel(selection SportsSelection) (tvapi.Channel, error) {

	if selection.ChannelID != "" && selection.ChannelID != "match" {

		catalog, err := r.tv.ListChannels()

		if err != nil {

			return tvapi.Channel{}, err

		}

		if channel, ok := catalog.FindByID(selection.ChannelID); ok {

			return channel, nil

		}

	}

	if selection.MatchID != "" {

		events, err := r.tv.Sports()

		if err != nil {

			return tvapi.Channel{}, err

		}

		for _, event := range events {

			if event.ID != selection.MatchID {

				continue

			}

			if channel, ok := r.tv.ResolveMatchChannel(event); ok {

				return channel, nil

			}

			break

		}

	}

	// Fall back to title search against the live schedule.
	matches, err := r.SportsSearch(selection.Title, 1)

	if err != nil {

		return tvapi.Channel{}, err

	}

	if len(matches) == 0 {

		return tvapi.Channel{}, fmt.Errorf("no streamable channel for game")

	}

	channel, ok := r.tv.ResolveMatchChannel(matches[0])

	if !ok {

		return tvapi.Channel{}, fmt.Errorf("no streamable channel for game")

	}

	return channel, nil

}

func (r *Resolver) IsPotentialSportsQuery(query string) bool {

	selection, err := r.ResolveTVSelection(query)

	if err != nil || selection == nil {

		return false

	}

	return tvapi.IsSportsChannel(selection.Channel)

}

func (r *Resolver) SearchTeams(query string, limit int) ([]string, error) {

	return r.tv.Teams(query, limit)

}

func (r *Resolver) FindTeamEvent(team string) (tvapi.SportsEvent, bool) {

	return r.tv.FindTeamEvent(team)

}

func (r *Resolver) ResolveMatchChannel(event tvapi.SportsEvent) (tvapi.Channel, bool) {

	return r.tv.ResolveMatchChannel(event)

}

func (r *Resolver) ResolveMatchChannelForTeam(event tvapi.SportsEvent, team string) (tvapi.Channel, bool) {

	return r.tv.ResolveMatchChannelForTeam(event, team)

}

func SportsLabel(event tvapi.SportsEvent) string {

	title := event.Title

	if title == "" {

		switch {

		case event.HomeTeam != "" && event.AwayTeam != "":

			title = event.HomeTeam + " vs " + event.AwayTeam

		case event.HomeTeam != "":

			title = event.HomeTeam

		default:

			title = "Live Sports"

		}

	}

	status := sportsStatusLabel(event)

	if event.League != "" {

		return fmt.Sprintf("%s • %s · %s", event.League, title, status)

	}

	return fmt.Sprintf("%s · %s", title, status)

}

func sportsStatusLabel(event tvapi.SportsEvent) string {

	if event.Live {

		return "Live"

	}

	if event.Start.IsZero() {

		if event.Time != "" {

			return event.Time

		}

		return "TBD"

	}

	now := time.Now()
	local := event.Start.Local()

	if local.Year() == now.Year() && local.YearDay() == now.YearDay() {

		return local.Format("3:04 PM")

	}

	if local.Year() == now.Year() && local.YearDay() == now.YearDay()+1 {

		return "Tomorrow " + local.Format("3:04 PM")

	}

	return local.Format("Mon 3:04 PM")

}

func SportsChannel(selection SportsSelection) tvapi.Channel {

	name := selection.Title

	if name == "" {

		name = "Live Sports"

	}

	return tvapi.Channel{

		ID:       selection.ChannelID,
		Name:     name,
		Category: fallback(selection.League, "Sports"),
	}

}

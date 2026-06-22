package tvapi

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const sportsTTL = 3 * time.Minute

var (

	sportsCategoryRE = regexp.MustCompile(`(?s)schedule__catHeader.*?card__meta">(.*?)</div>`)
	sportsEventRE = regexp.MustCompile(`(?s)data-time="([^"]*)"[^>]*>[^<]*</span>\s*<span class="schedule__eventTitle">([^<]*)</span>.*?<div class="schedule__channels">(.*?)</div>`)
	sportsChannelRE = regexp.MustCompile(`href="/watch\.php\?id=(\d+)"[^>]*\btitle="([^"]*)"`)

)

// skippedSportsCategories are listing groups on the schedule that are not games.
var skippedSportsCategories = map[string]struct{}{

	"tv shows": {},

}

type SportsChannel struct {

	DaddyID string
	Name string

}

type SportsEvent struct {

	Title string
	League string

	Time string
	Start time.Time

	Channels []SportsChannel

}

func (c *TVClient) Sports() ([]SportsEvent, error) {

	c.sportsMu.RLock()

	events := c.sports
	fresh := events != nil && time.Now().Before(c.sportsAt.Add(sportsTTL))

	c.sportsMu.RUnlock()

	if fresh {

		return events, nil

	}

	// Serve stale data immediately and refresh in the background so callers
	// (notably autocomplete) never block on a live scrape.
	if events != nil {

		c.refreshSportsAsync()
		return events, nil

	}

	return c.refreshSports()

}

func (c *TVClient) refreshSportsAsync() {

	c.sportsMu.Lock()

	if c.sportsRefreshing {

		c.sportsMu.Unlock()
		return

	}

	c.sportsRefreshing = true
	c.sportsMu.Unlock()

	go func() { _, _ = c.refreshSports() }()

}

func (c *TVClient) refreshSports() ([]SportsEvent, error) {

	events, err := c.fetchSports()

	c.sportsMu.Lock()
	c.sportsRefreshing = false

	if err != nil {

		stale := c.sports
		c.sportsMu.Unlock()

		if stale != nil {

			return stale, nil

		}

		return nil, err

	}

	c.sports = events
	c.sportsAt = time.Now()
	c.sportsMu.Unlock()

	return events, nil

}

func (c *TVClient) WarmupSports() {

	go func() { _, _ = c.Sports() }()

}

func (c *TVClient) fetchSports() ([]SportsEvent, error) {

	base := strings.TrimRight(dlhdBaseURL(), "/")

	if base == "" {

		base = dlhdDefaultBaseURL

	}

	response, err := c.get(base+"/", base+"/")

	if err != nil {

		return nil, fmt.Errorf("sports scrape: fetch: %w", err)

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		return nil, fmt.Errorf("sports scrape: status %d", response.StatusCode)

	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 12<<20))

	if err != nil {

		return nil, fmt.Errorf("sports scrape: read: %w", err)

	}

	return parseSportsSchedule(string(body)), nil

}

func parseSportsSchedule(page string) []SportsEvent {

	categories := sportsCategoryRE.FindAllStringSubmatchIndex(page, -1)
	events := sportsEventRE.FindAllStringSubmatchIndex(page, -1)

	loc := londonLocation()
	now := time.Now()

	seen := make(map[string]struct{}, len(events))
	parsed := make([]SportsEvent, 0, len(events))

	for _, m := range events {

		league := html.UnescapeString(strings.TrimSpace(categoryBefore(page, categories, m[0])))
		timeText := strings.TrimSpace(page[m[2]:m[3]])
		title := html.UnescapeString(strings.TrimSpace(page[m[4]:m[5]]))
		channelsHTML := page[m[6]:m[7]]

		if title == "" {

			continue

		}

		if _, skip := skippedSportsCategories[strings.ToLower(league)]; skip {

			continue

		}

		channels := parseSportsChannels(channelsHTML)

		if len(channels) == 0 {

			continue

		}

		key := strings.ToLower(league + "|" + title + "|" + timeText)

		if _, dup := seen[key]; dup {

			continue

		}

		seen[key] = struct{}{}

		parsed = append(parsed, SportsEvent{

			Title: title,
			League: cleanLeague(league, title),

			Time: timeText,
			Start: parseScheduleStart(timeText, loc, now),

			Channels: channels,

		})

	}

	sortSportsEvents(parsed, now)

	return parsed

}

func parseSportsChannels(fragment string) []SportsChannel {

	matches := sportsChannelRE.FindAllStringSubmatch(fragment, -1)

	seen := make(map[string]struct{}, len(matches))
	channels := make([]SportsChannel, 0, len(matches))

	for _, m := range matches {

		daddyID := m[1]

		if _, dup := seen[daddyID]; dup {

			continue

		}

		seen[daddyID] = struct{}{}

		channels = append(channels, SportsChannel{

			DaddyID: daddyID,
			Name: html.UnescapeString(strings.TrimSpace(m[2])),

		})

	}

	return channels

}

func categoryBefore(page string, categories [][]int, eventStart int) string {

	league := ""

	for _, c := range categories {

		if c[0] >= eventStart {

			break

		}

		league = page[c[2]:c[3]]

	}

	return league

}

func cleanLeague(league, title string) string {

	league = strings.TrimSpace(league)

	if league == "" || strings.EqualFold(league, title) || strings.EqualFold(league, "other") {

		return ""

	}

	return league

}

func parseScheduleStart(timeText string, loc *time.Location, now time.Time) time.Time {

	parts := strings.SplitN(strings.TrimSpace(timeText), ":", 2)

	if len(parts) != 2 {

		return time.Time{}

	}

	hour, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	minute, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))

	if err1 != nil || err2 != nil {

		return time.Time{}

	}

	local := now.In(loc)

	return time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, loc)

}

// sortSportsEvents orders live games first, then soonest upcoming, then past.
func sortSportsEvents(events []SportsEvent, now time.Time) {

	bucket := func(e SportsEvent) int {

		if e.Start.IsZero() {

			return 3

		}

		delta := now.Sub(e.Start)

		switch {

		case delta >= 0 && delta <= 3*time.Hour:

			return 0

		case delta < 0:

			return 1

		default:

			return 2

		}

	}

	sort.SliceStable(events, func(i, j int) bool {

		bi := bucket(events[i])
		bj := bucket(events[j])

		if bi != bj {

			return bi < bj

		}

		switch bi {

		case 1:

			return events[i].Start.Before(events[j].Start)

		case 2:

			return events[i].Start.After(events[j].Start)

		default:

			return events[i].Start.Before(events[j].Start)

		}

	})

}

func londonLocation() *time.Location {

	loc, err := time.LoadLocation("Europe/London")

	if err != nil {

		return time.UTC

	}

	return loc

}

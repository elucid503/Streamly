package tvapi

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	sportsTTL    = 2 * time.Minute
	sportsServer = "kobra"
)

var (
	sourceSelectOptionRE = regexp.MustCompile(`(?is)<option[^>]*>(.*?)</option>`)
)

type SportsChannel struct {
	ChannelID string
	Name      string
}

type SportsEvent struct {
	ID     string
	Title  string
	League string

	Time  string
	Start time.Time
	Live  bool

	HomeTeam string
	AwayTeam string

	Channels []SportsChannel
}

type ntvMatchesResponse struct {
	Success bool       `json:"success"`
	Live    []ntvMatch `json:"live"`
	NonLive []ntvMatch `json:"nonLive"`
	All     []ntvMatch `json:"all"`
}

type ntvMatch struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Category string `json:"category"`
	Date     int64  `json:"date"`
	Live     bool   `json:"live"`

	Teams ntvMatchTeams `json:"teams"`
}

type ntvMatchTeams struct {
	Home ntvMatchTeam `json:"home"`
	Away ntvMatchTeam `json:"away"`
}

type ntvMatchTeam struct {
	Name string `json:"name"`
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
	// (notably autocomplete) never block on a live fetch.
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

	// Team-channel matching needs the catalog; best-effort warm if empty.
	if c.anyCatalog() == nil {

		_, _ = c.fetchCatalog(catalogRefreshTimeout)

	}

	url := fmt.Sprintf("%s/api/get-matches?server=%s&type=both", c.baseURL, sportsServer)

	response, err := c.get(url, c.baseURL+"/")

	if err != nil {

		return nil, fmt.Errorf("sports fetch: %w", err)

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		return nil, fmt.Errorf("sports fetch: status %d", response.StatusCode)

	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))

	if err != nil {

		return nil, fmt.Errorf("sports fetch: read: %w", err)

	}

	var payload ntvMatchesResponse

	if err := json.Unmarshal(body, &payload); err != nil {

		return nil, fmt.Errorf("sports fetch: decode: %w", err)

	}

	events := c.mergeNtvMatches(payload)
	now := time.Now()

	sortSportsEvents(events, now)

	return events, nil

}

func (c *TVClient) mergeNtvMatches(payload ntvMatchesResponse) []SportsEvent {

	seen := make(map[string]struct{}, len(payload.All)+len(payload.Live)+len(payload.NonLive))
	events := make([]SportsEvent, 0, len(payload.All)+len(payload.Live))

	appendMatch := func(raw ntvMatch, forceLive bool) {

		id := strings.TrimSpace(raw.ID)

		if id == "" {

			return

		}

		if _, dup := seen[id]; dup {

			// A later live copy should upgrade the earlier entry.
			if forceLive || raw.Live {

				for i := range events {

					if events[i].ID == id {

						events[i].Live = true
						break

					}

				}

			}

			return

		}

		seen[id] = struct{}{}

		home := strings.TrimSpace(raw.Teams.Home.Name)
		away := strings.TrimSpace(raw.Teams.Away.Name)
		title := strings.TrimSpace(raw.Title)

		if title == "" {

			switch {

			case home != "" && away != "":

				title = home + " vs " + away

			case home != "":

				title = home

			case away != "":

				title = away

			default:

				return

			}

		}

		start := time.Time{}

		if raw.Date > 0 {

			start = time.UnixMilli(raw.Date).UTC()

		}

		league := cleanLeague(raw.Category, title)
		live := forceLive || raw.Live

		event := SportsEvent{

			ID:     id,
			Title:  title,
			League: league,

			Time:  formatSportsClock(start),
			Start: start,
			Live:  live,

			HomeTeam: home,
			AwayTeam: away,

			Channels: c.matchChannelsForEvent(home, away),
		}

		events = append(events, event)

	}

	for _, raw := range payload.Live {

		appendMatch(raw, true)

	}

	for _, raw := range payload.NonLive {

		appendMatch(raw, false)

	}

	for _, raw := range payload.All {

		appendMatch(raw, false)

	}

	return events

}

// matchChannelsForEvent finds 24/7 catalog channels for a fixture via exact
// home/away team channel names. Broadcaster scrape is done on demand at play time.
func (c *TVClient) matchChannelsForEvent(home, away string) []SportsChannel {

	catalog := c.cachedCatalog()

	if catalog == nil {

		return nil

	}

	seen := make(map[string]struct{}, 2)
	channels := make([]SportsChannel, 0, 2)

	for _, team := range []string{home, away} {

		team = strings.TrimSpace(team)

		if team == "" {

			continue

		}

		channel, ok := catalog.FindByName(team)

		if !ok {

			continue

		}

		if _, dup := seen[channel.ID]; dup {

			continue

		}

		seen[channel.ID] = struct{}{}

		channels = append(channels, SportsChannel{

			ChannelID: channel.ID,
			Name:      channel.Name,
		})

	}

	return channels

}

// ResolveMatchChannel picks a streamable 24/7 channel for a sports fixture:
// (1) exact home/away team channel name, else (2) broadcaster labels from the
// watch page matched against the channel catalog.
func (c *TVClient) ResolveMatchChannel(event SportsEvent) (Channel, bool) {

	return c.ResolveMatchChannelForTeam(event, "")

}

// ResolveMatchChannelForTeam prefers the named team's dedicated channel when present.
func (c *TVClient) ResolveMatchChannelForTeam(event SportsEvent, preferTeam string) (Channel, bool) {

	catalog := c.cachedCatalog()

	if catalog == nil {

		return Channel{}, false

	}

	preferTeam = strings.TrimSpace(preferTeam)

	if preferTeam != "" {

		if channel, ok := catalog.FindByName(preferTeam); ok {

			return channel, true

		}

	}

	for _, linked := range event.Channels {

		if channel, ok := catalog.FindByID(linked.ChannelID); ok {

			return channel, true

		}

	}

	for _, team := range []string{event.HomeTeam, event.AwayTeam} {

		if channel, ok := catalog.FindByName(team); ok {

			return channel, true

		}

	}

	broadcasters := c.scrapeMatchBroadcasters(event.ID)

	for _, name := range broadcasters {

		if channel, ok := catalog.FindByName(name); ok {

			return channel, true

		}

		hits := catalog.Search(name, 1)

		if len(hits) > 0 {

			return hits[0], true

		}

	}

	return Channel{}, false

}

func (c *TVClient) scrapeMatchBroadcasters(matchID string) []string {

	matchID = strings.TrimSpace(matchID)

	if matchID == "" {

		return nil

	}

	watchURL := fmt.Sprintf("%s/watch/%s/%s", c.baseURL, sportsServer, matchID)

	response, err := c.get(watchURL, c.baseURL+"/")

	if err != nil {

		return nil

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		return nil

	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))

	if err != nil {

		return nil

	}

	return parseSourceSelectBroadcasters(string(body))

}

func parseSourceSelectBroadcasters(page string) []string {

	// Prefer the sourceSelect block when present.
	lower := strings.ToLower(page)
	start := strings.Index(lower, `id="sourceselect"`)

	if start < 0 {

		start = strings.Index(lower, "id='sourceselect'")

	}

	fragment := page

	if start >= 0 {

		end := strings.Index(lower[start:], "</select>")

		if end > 0 {

			fragment = page[start : start+end]

		}

	}

	matches := sourceSelectOptionRE.FindAllStringSubmatch(fragment, -1)
	seen := make(map[string]struct{}, len(matches))
	names := make([]string, 0, len(matches))

	for _, m := range matches {

		label := html.UnescapeString(strings.TrimSpace(stripTags(m[1])))

		if label == "" {

			continue

		}

		broadcaster := broadcasterFromSourceLabel(label)

		if broadcaster == "" {

			continue

		}

		key := strings.ToLower(broadcaster)

		if _, dup := seen[key]; dup {

			continue

		}

		seen[key] = struct{}{}
		names = append(names, broadcaster)

	}

	return names

}

// broadcasterFromSourceLabel parses labels like
// "Server Kobra - ADMIN - English - NBC Sports BA - Stream 1 [HD]".
func broadcasterFromSourceLabel(label string) string {

	parts := strings.Split(label, " - ")

	for i := range parts {

		parts[i] = strings.TrimSpace(parts[i])

	}

	if len(parts) < 4 {

		return ""

	}

	// Skip source-only labels (ADMIN/ECHO/GOLF) without a channel name.
	name := parts[3]

	if name == "" {

		return ""

	}

	// Drop trailing "Stream N" segments if the label was over-split.
	if strings.HasPrefix(strings.ToLower(name), "stream ") {

		return ""

	}

	return name

}

func stripTags(value string) string {

	var b strings.Builder

	inTag := false

	for _, r := range value {

		switch {

		case r == '<':

			inTag = true

		case r == '>':

			inTag = false

		case !inTag:

			b.WriteRune(r)

		}

	}

	return b.String()

}

func cleanLeague(league, title string) string {

	league = strings.TrimSpace(league)

	if league == "" || strings.EqualFold(league, title) || strings.EqualFold(league, "other") {

		return ""

	}

	league = strings.ReplaceAll(league, "-", " ")
	words := strings.Fields(league)

	for i, word := range words {

		if word == "" {

			continue

		}

		words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])

	}

	return strings.Join(words, " ")

}

func formatSportsClock(start time.Time) string {

	if start.IsZero() {

		return ""

	}

	local := start.Local()

	return local.Format("3:04 PM")

}

// sortSportsEvents orders live games first, then soonest upcoming, then past.
func sortSportsEvents(events []SportsEvent, now time.Time) {

	bucket := func(e SportsEvent) int {

		if e.Live {

			return 0

		}

		if e.Start.IsZero() {

			return 3

		}

		if e.Start.After(now) {

			return 1

		}

		// Recently started without live flag still ranks ahead of older past games.
		if now.Sub(e.Start) <= 3*time.Hour {

			return 0

		}

		return 2

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

// Teams returns unique team names from the sports schedule for autocomplete.
func (c *TVClient) Teams(query string, limit int) ([]string, error) {

	events, err := c.Sports()

	if err != nil {

		return nil, err

	}

	if limit <= 0 {

		limit = 25

	}

	query = strings.ToLower(strings.TrimSpace(query))
	seen := make(map[string]struct{}, 64)
	teams := make([]string, 0, limit)

	add := func(name string) {

		name = strings.TrimSpace(name)

		if name == "" {

			return

		}

		key := strings.ToLower(name)

		if _, dup := seen[key]; dup {

			return

		}

		if query != "" && !strings.Contains(key, query) {

			return

		}

		seen[key] = struct{}{}
		teams = append(teams, name)

	}

	for _, event := range events {

		add(event.HomeTeam)
		add(event.AwayTeam)

		if len(teams) >= limit {

			break

		}

	}

	// When the user is typing, supplement with catalog names so teams without a
	// scheduled match (and dedicated 24/7 channels) still autocomplete.
	if query != "" && len(teams) < limit {

		if catalog := c.cachedCatalog(); catalog != nil {

			for _, channel := range catalog.Search(query, limit*2) {

				// Prefer multi-word names (typical team channels over "ESPN").
				if strings.Count(strings.TrimSpace(channel.Name), " ") < 1 {

					continue

				}

				add(channel.Name)

				if len(teams) >= limit {

					break

				}

			}

		}

	}

	if len(teams) > limit {

		teams = teams[:limit]

	}

	return teams, nil

}

// FindTeamEvent finds a live/starting event involving the given team.
func (c *TVClient) FindTeamEvent(team string) (SportsEvent, bool) {

	team = strings.ToLower(strings.TrimSpace(team))

	if team == "" {

		return SportsEvent{}, false

	}

	events, err := c.Sports()

	if err != nil {

		return SportsEvent{}, false

	}

	now := time.Now()

	for _, event := range events {

		if !teamInEvent(event, team) {

			continue

		}

		if sportsEventActive(event, now) {

			return event, true

		}

	}

	return SportsEvent{}, false

}

func teamInEvent(event SportsEvent, teamLower string) bool {

	return strings.EqualFold(event.HomeTeam, teamLower) ||
		strings.EqualFold(event.AwayTeam, teamLower) ||
		strings.Contains(strings.ToLower(event.HomeTeam), teamLower) ||
		strings.Contains(strings.ToLower(event.AwayTeam), teamLower) ||
		strings.Contains(strings.ToLower(event.Title), teamLower)

}

// sportsEventActive is true for live fixtures and ones that just started or are about to.
func sportsEventActive(event SportsEvent, now time.Time) bool {

	if event.Live {

		return true

	}

	if event.Start.IsZero() {

		return false

	}

	// Join a couple minutes early through a few hours after tip-off.
	if now.Before(event.Start.Add(-2 * time.Minute)) {

		return false

	}

	return now.Sub(event.Start) <= 3*time.Hour

}

// Opponent returns the non-subscribed team name for announce messages.
func (event SportsEvent) Opponent(team string) string {

	team = strings.TrimSpace(team)

	if team == "" {

		return fallbackTeam(event)

	}

	if strings.EqualFold(event.HomeTeam, team) {

		return fallback(event.AwayTeam, fallbackTeam(event))

	}

	if strings.EqualFold(event.AwayTeam, team) {

		return fallback(event.HomeTeam, fallbackTeam(event))

	}

	// Partial match: prefer the side that did not match.
	lower := strings.ToLower(team)

	if strings.Contains(strings.ToLower(event.HomeTeam), lower) {

		return fallback(event.AwayTeam, fallbackTeam(event))

	}

	if strings.Contains(strings.ToLower(event.AwayTeam), lower) {

		return fallback(event.HomeTeam, fallbackTeam(event))

	}

	return fallbackTeam(event)

}

func fallbackTeam(event SportsEvent) string {

	if event.AwayTeam != "" {

		return event.AwayTeam

	}

	if event.HomeTeam != "" {

		return event.HomeTeam

	}

	return "Opponent"

}

func fallback(value, defaultValue string) string {

	if strings.TrimSpace(value) == "" {

		return defaultValue

	}

	return value

}

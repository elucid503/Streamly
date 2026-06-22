package tvmaze

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"streamly/internal/textutil"
)

const guideTTL = 30 * time.Minute

type Program struct {

	Show string
	Episode string

	Start time.Time
	End time.Time

}

type networkGuide struct {

	norm string
	programs []Program

}

type scheduleItem struct {

	Name string `json:"name"`
	Season int `json:"season"`
	Number int `json:"number"`

	Airstamp string `json:"airstamp"`
	Runtime int `json:"runtime"`

	Show struct {

		Name string `json:"name"`

		Network *struct{ Name string `json:"name"` } `json:"network"`
		WebChannel *struct{ Name string `json:"name"` } `json:"webChannel"`

	} `json:"show"`

}

// NowNext returns the show airing now and the one airing next on the named
// channel, best-effort. Either may be nil when the guide has no match.
func (c *Client) NowNext(channelName string) (*Program, *Program) {

	guide := c.ensureGuide()

	if guide == nil {

		return nil, nil

	}

	programs := matchNetwork(guide, channelName)

	if len(programs) == 0 {

		return nil, nil

	}

	now := time.Now()

	var current *Program
	var next *Program

	for index := range programs {

		program := &programs[index]

		if !program.Start.After(now) && program.End.After(now) {

			current = program
			continue

		}

		if program.Start.After(now) {

			next = program
			break

		}

	}

	return current, next

}

func (c *Client) Warmup() {

	go c.ensureGuide()

}

// ensureGuide returns the cached guide (possibly stale or nil) and never blocks;
// a refresh is kicked off in the background when the cache is stale so the
// latency-sensitive autocomplete path is never held up by a live fetch.
func (c *Client) ensureGuide() []networkGuide {

	c.mu.Lock()

	guide := c.guide
	fresh := guide != nil && time.Now().Before(c.guideExpiry)

	if fresh || c.guideRefreshing {

		c.mu.Unlock()
		return guide

	}

	c.guideRefreshing = true
	c.mu.Unlock()

	go func() {

		fetched, err := c.fetchGuide()

		c.mu.Lock()
		c.guideRefreshing = false

		if err == nil {

			c.guide = fetched
			c.guideExpiry = time.Now().Add(guideTTL)

		}

		c.mu.Unlock()

	}()

	return guide

}

func (c *Client) fetchGuide() ([]networkGuide, error) {

	today := time.Now().UTC()

	var items []scheduleItem

	for _, day := range []time.Time{today, today.AddDate(0, 0, 1)} {

		var batch []scheduleItem

		url := fmt.Sprintf("%s/schedule?country=US&date=%s", tvmazeBaseURL(), day.Format("2006-01-02"))

		if err := c.getJSON(url, &batch); err != nil {

			if len(items) == 0 {

				return nil, err

			}

			continue

		}

		items = append(items, batch...)

	}

	byNetwork := make(map[string][]Program)

	for _, item := range items {

		network := ""

		if item.Show.Network != nil {

			network = item.Show.Network.Name

		} else if item.Show.WebChannel != nil {

			network = item.Show.WebChannel.Name

		}

		if network == "" || item.Show.Name == "" {

			continue

		}

		start, err := time.Parse(time.RFC3339, item.Airstamp)

		if err != nil {

			continue

		}

		runtime := item.Runtime

		if runtime <= 0 {

			runtime = 30

		}

		key := normalizeNetwork(network)

		byNetwork[key] = append(byNetwork[key], Program{

			Show: textutil.DecodeHTML(item.Show.Name),
			Episode: episodeLabel(item),

			Start: start,
			End: start.Add(time.Duration(runtime) * time.Minute),

		})

	}

	guide := make([]networkGuide, 0, len(byNetwork))

	for key, programs := range byNetwork {

		sort.Slice(programs, func(i, j int) bool { return programs[i].Start.Before(programs[j].Start) })

		guide = append(guide, networkGuide{norm: key, programs: programs})

	}

	return guide, nil

}

func episodeLabel(item scheduleItem) string {

	if item.Season > 0 && item.Number > 0 {

		return fmt.Sprintf("S%dE%d", item.Season, item.Number)

	}

	return ""

}

func matchNetwork(guide []networkGuide, channelName string) []Program {

	cn := normalizeNetwork(channelName)

	if cn == "" {

		return nil

	}

	base := strings.TrimSuffix(strings.TrimSuffix(cn, "usa"), "us")

	for _, ng := range guide {

		if ng.norm == cn || ng.norm == base {

			return ng.programs

		}

	}

	for _, ng := range guide {

		if len(ng.norm) >= 3 && (strings.HasPrefix(cn, ng.norm) || strings.HasPrefix(base, ng.norm)) {

			return ng.programs

		}

	}

	return nil

}

func normalizeNetwork(name string) string {

	name = strings.ToLower(name)

	var b strings.Builder

	for _, r := range name {

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {

			b.WriteRune(r)

		}

	}

	return b.String()

}

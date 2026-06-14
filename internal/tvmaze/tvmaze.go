package tvmaze

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"streamly/internal/textutil"
)

func tvmazeBaseURL() string {

	return strings.TrimRight(strings.TrimSpace(os.Getenv("TVMAZE_BASE_URL")), "/")

}

type Client struct {

	http *http.Client

	mu sync.Mutex
	idCache map[string]int
	epCache map[int]epEntry

}

type epEntry struct {

	eps []tvEpisode

	expiry time.Time

}

type tvEpisode struct {

	Season int `json:"season"`
	Number int `json:"number"`

	Name string `json:"name"`

}

const epCacheTTL = 24 * time.Hour

func NewClient() *Client {

	return &Client{

		http: &http.Client{Timeout: 8 * time.Second},

		idCache: make(map[string]int),
		epCache: make(map[int]epEntry),

	}

}

func (c *Client) EpisodeTitles(imdbID string, season int) (map[int]string, error) {

	tvID, err := c.resolveID(imdbID)

	if err != nil {

		return nil, err

	}

	eps, err := c.episodes(tvID)

	if err != nil {

		return nil, err

	}

	titles := make(map[int]string)

	for _, ep := range eps {

		if ep.Season == season && ep.Number > 0 && ep.Name != "" {

			titles[ep.Number] = textutil.DecodeHTML(ep.Name)

		}

	}

	return titles, nil

}

func (c *Client) resolveID(imdbID string) (int, error) {

	c.mu.Lock()

	if id, ok := c.idCache[imdbID]; ok {

		c.mu.Unlock()

		return id, nil

	}

	c.mu.Unlock()

	var show struct {

		ID int `json:"id"`

	}

	if err := c.getJSON(fmt.Sprintf("%s/lookup/shows?imdb=%s", tvmazeBaseURL(), imdbID), &show); err != nil {

		return 0, err

	}

	if show.ID == 0 {

		return 0, fmt.Errorf("tvmaze: show not found for imdb=%s", imdbID)

	}

	c.mu.Lock()

	c.idCache[imdbID] = show.ID

	c.mu.Unlock()

	return show.ID, nil

}

func (c *Client) episodes(tvID int) ([]tvEpisode, error) {

	c.mu.Lock()

	if entry, ok := c.epCache[tvID]; ok && time.Now().Before(entry.expiry) {

		eps := entry.eps

		c.mu.Unlock()

		return eps, nil

	}

	c.mu.Unlock()

	var eps []tvEpisode

	if err := c.getJSON(fmt.Sprintf("%s/shows/%d/episodes", tvmazeBaseURL(), tvID), &eps); err != nil {

		return nil, err

	}

	c.mu.Lock()

	c.epCache[tvID] = epEntry{

		eps: eps,
		expiry: time.Now().Add(epCacheTTL),

	}

	c.mu.Unlock()

	return eps, nil

}

func (c *Client) getJSON(url string, dest any) error {

	resp, err := c.http.Get(url)

	if err != nil {

		return err

	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)

	if err != nil {

		return err

	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {

		return fmt.Errorf("tvmaze: %s → %s", url, resp.Status)

	}

	return json.Unmarshal(body, dest)

}

package introdb

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/imroc/req/v3"
)

const (
	defaultBaseURL = "https://api.theintrodb.org/v3"
	userAgent      = "streamly/1.0"
)

// ClientOptions tunes a Client instance.
type ClientOptions struct {
	BaseURL string
	APIKey  string
}

// MediaQuery identifies a movie or TV episode for intro timing lookup.
type MediaQuery struct {
	TMDBId     int
	IMDBId     string
	Season     int
	Episode    int
	DurationMs int64
}

// SegmentTimestamp is one community-verified intro/recap/credits window.
type SegmentTimestamp struct {
	StartMs int64
	EndMs   *int64
}

// MediaRecord is the normalized intro timing payload from TheIntroDB.
type MediaRecord struct {
	TMDBId int
	Type   string
	Intro  []SegmentTimestamp
}

// Client fetches segment timestamps from TheIntroDB.
type Client struct {
	baseURL string
	apiKey  string
	http    *req.Client
}

// NewClient builds a Client with sensible defaults.
func NewClient(options ...ClientOptions) *Client {

	client := &Client{
		baseURL: defaultBaseURL,
		http: req.C().
			SetTimeout(15 * time.Second).
			SetUserAgent(userAgent).
			ImpersonateChrome(),
	}

	if len(options) > 0 {
		if baseURL := strings.TrimSpace(options[0].BaseURL); baseURL != "" {
			client.baseURL = strings.TrimRight(baseURL, "/")
		}
		client.apiKey = strings.TrimSpace(options[0].APIKey)
	}

	return client

}

// GetMedia fetches intro timings for the given title. Reads are public; an API key is
// optional but includes the requester's pending submissions in weighted results.
func (c *Client) GetMedia(query MediaQuery) (*MediaRecord, error) {

	if query.TMDBId <= 0 && strings.TrimSpace(query.IMDBId) == "" {
		return nil, fmt.Errorf("introdb: tmdb or imdb id required")
	}

	request := c.http.R().SetHeader("Accept", "application/json")

	if c.apiKey != "" {
		request.SetBearerAuthToken(c.apiKey)
	}

	params := map[string]string{}

	if query.TMDBId > 0 {
		params["tmdb_id"] = strconv.Itoa(query.TMDBId)
	}

	if id := strings.TrimSpace(query.IMDBId); id != "" {
		params["imdb_id"] = id
	}

	if query.Season > 0 && query.Episode > 0 {
		params["season"] = strconv.Itoa(query.Season)
		params["episode"] = strconv.Itoa(query.Episode)
	}

	if query.DurationMs > 0 {
		params["duration_ms"] = strconv.FormatInt(query.DurationMs, 10)
	}

	request.SetQueryParams(params)

	response, err := request.Get(c.baseURL + "/media")
	if err != nil {
		return nil, err
	}

	body := response.String()

	if response.StatusCode != 200 {
		return nil, apiErrorFromStatus(response.StatusCode, body)
	}

	var raw mediaResponseRaw
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return nil, err
	}

	return parseMediaResponse(raw), nil

}

type mediaResponseRaw struct {
	TMDBId int                   `json:"tmdb_id"`
	Type   string                `json:"type"`
	Intro  []segmentTimestampRaw `json:"intro"`
}

type segmentTimestampRaw struct {
	StartMs *int64 `json:"start_ms"`
	EndMs   *int64 `json:"end_ms"`
}
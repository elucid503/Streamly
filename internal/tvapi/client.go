package tvapi

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultTVBaseURL = "https://ntv.cx"
	catalogTTL       = 15 * time.Minute
)

type TVOptions struct {
	BaseURL string
}

type TVClient struct {
	baseURL string
	client  *http.Client

	catalogMu sync.RWMutex
	catalog   *ChannelCatalog
	catalogAt time.Time

	refreshOnce sync.Once

	metadataMu sync.RWMutex
	metadata   *channelMetadataIndex
	enrichOnce sync.Once

	sportsMu         sync.RWMutex
	sports           []SportsEvent
	sportsAt         time.Time
	sportsRefreshing bool
}

func NewTVClient(options TVOptions) *TVClient {

	baseURL := options.BaseURL

	if baseURL == "" {

		baseURL = os.Getenv("TV_BASE_URL")

	}

	if baseURL == "" {

		baseURL = defaultTVBaseURL

	}

	client := &TVClient{

		baseURL: strings.TrimRight(baseURL, "/"),

		client: &http.Client{

			Timeout: 30 * time.Second,
		},
	}

	seedEmbeddedCatalog(client)

	return client

}

func (c *TVClient) BaseURL() string {

	return c.baseURL

}

func (c *TVClient) ResolveHLS(channelID string) (string, error) {

	stream, err := c.ResolveStream(channelID)

	if err != nil {

		return "", err

	}

	return stream.URL, nil

}

func (c *TVClient) ResolveChannel(channel Channel) (*StreamInfo, error) {

	id := strings.TrimSpace(channel.ID)

	if id == "" {

		return nil, fmt.Errorf("channel id is required")

	}

	hls, err := c.ResolveHLS(id)

	if err != nil {

		return nil, err

	}

	return &StreamInfo{

		Channel: channel,
		HLSURL:  hls,
	}, nil

}

func (c *TVClient) cachedCatalog() *ChannelCatalog {

	return c.anyCatalog()

}

func (c *TVClient) storeCatalog(catalog *ChannelCatalog) {

	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()

	c.catalog = catalog

	c.catalogAt = time.Now()

}

const tvBrowserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

func (c *TVClient) get(rawURL, referer string) (*http.Response, error) {

	request, err := http.NewRequest(http.MethodGet, rawURL, nil)

	if err != nil {

		return nil, err

	}

	if referer == "" {

		referer = c.baseURL + "/"

	}

	request.Header.Set("User-Agent", tvBrowserUA)
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")

	request.Header.Set("Referer", referer)

	return c.client.Do(request)

}

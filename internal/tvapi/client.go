package tvapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL = "https://dami-tv.pro"
	channelsPath = "/data/tv-channels.json?v=302"
	legacyResolvePath = "/papi/tv/resolve/"

	catalogTTL = 15 * time.Minute
)

type TVOptions struct {

	BaseURL string
}

type TVClient struct {

	baseURL string
	client *http.Client

	catalogMu sync.RWMutex
	catalog *ChannelCatalog
	catalogAt time.Time

	refreshOnce sync.Once
}

func NewTVClient(options TVOptions) *TVClient {

	baseURL := options.BaseURL

	if baseURL == "" {

		baseURL = os.Getenv("TV_BASE_URL")
	}

	if baseURL == "" {

		baseURL = defaultBaseURL
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

func (c *TVClient) ResolveHLS(daddyID string) (string, error) {

	stream, err := c.ResolveStream(daddyID)

	if err != nil {

		return "", err
	}

	return stream.URL, nil

}

func parseResolveResponse(body []byte) (string, error) {

	var tv247 TV247ResolveResult

	if err := json.Unmarshal(body, &tv247); err == nil {

		if tv247.Error != "" {

			return "", fmt.Errorf("resolve failed: %s", tv247.Error)
		}

		if tv247.ProxyPlaylistURL != "" {

			return tv247.ProxyPlaylistURL, nil
		}

	}

	var legacy ResolveResult

	if err := json.Unmarshal(body, &legacy); err != nil {

		return "", fmt.Errorf("decode resolve response: %w", err)
	}

	if !legacy.Success {

		msg := legacy.Error

		if msg == "" {

			msg = string(body)
		}

		return "", fmt.Errorf("resolve failed: %s", msg)

	}

	if legacy.Stream == "" {

		return "", nil
	}

	streamPath := legacy.Stream

	if strings.HasPrefix(streamPath, "http://") || strings.HasPrefix(streamPath, "https://") {

		return streamPath, nil
	}

	if !strings.HasPrefix(streamPath, "/") {

		streamPath = "/" + streamPath
	}

	return streamPath, nil

}

func (c *TVClient) ResolveChannel(channel Channel) (*StreamInfo, error) {

	hls, err := c.ResolveHLS(channel.DaddyID)

	if err != nil {

		return nil, err
	}

	return &StreamInfo{

		Channel: channel,
		HLSURL: hls,
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

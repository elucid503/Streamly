package tvapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (

	defaultBaseURL = "https://dami-tv.pro"
	channelsPath = "/data/tv-channels.json?v=302"
	resolvePathPrefix = "/papi/tv/resolve/"

	catalogTTL = 15 * time.Minute // cache duraiton for the tv-channels.json catalog

)

// TVOptions tunes a TVClient instance.
type TVOptions struct {

	BaseURL string // API base URL. Defaults to TV_BASE_URL env or dami-tv.pro.

}

// TVClient fetches channel listings and resolves HLS streams from dami-tv.pro.
type TVClient struct {

	baseURL string
	client *http.Client

	catalogMu sync.RWMutex
	catalog *ChannelCatalog
	catalogAt time.Time

}

// NewTVClient builds a TVClient with optional overrides.
func NewTVClient(options TVOptions) *TVClient {

	baseURL := options.BaseURL

	if baseURL == "" {

		baseURL = os.Getenv("TV_BASE_URL")

	}

	if baseURL == "" {

		baseURL = defaultBaseURL

	}

	return &TVClient{

		baseURL: strings.TrimRight(baseURL, "/"),

		client: &http.Client{

			Timeout: 30 * time.Second,

		},

	}

}

// ListChannels downloads and parses the full TV channel catalog.
func (c *TVClient) ListChannels() (*ChannelCatalog, error) {

	if cached := c.cachedCatalog(); cached != nil {

		return cached, nil

	}

	u := c.baseURL + channelsPath

	response, err := c.client.Get(u)

	if err != nil {

		return nil, fmt.Errorf("fetch channels: %w", err)

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return nil, fmt.Errorf("fetch channels: status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))

	}

	var catalog ChannelCatalog

	if err := json.NewDecoder(response.Body).Decode(&catalog); err != nil {

		return nil, fmt.Errorf("decode channels: %w", err)

	}

	c.storeCatalog(&catalog)

	return &catalog, nil

}

// ResolveHLS turns a daddyId (e.g. "44") into a full proxied m3u8 URL.
func (c *TVClient) ResolveHLS(daddyID string) (string, error) {

	daddyID = strings.TrimSpace(daddyID)

	if daddyID == "" {

		return "", fmt.Errorf("daddyId is required")

	}

	u := c.baseURL + resolvePathPrefix + url.PathEscape(daddyID)

	response, err := c.client.Get(u)

	if err != nil {

		return "", fmt.Errorf("resolve stream: %w", err)

	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)

	if err != nil {

		return "", fmt.Errorf("read resolve response: %w", err)

	}

	var result ResolveResult

	if err := json.Unmarshal(body, &result); err != nil {

		return "", fmt.Errorf("decode resolve response: %w", err)

	}

	if !result.Success {

		msg := result.Error

		if msg == "" {

			msg = string(body)

		}

		return "", fmt.Errorf("resolve failed: %s", msg)

	}

	if result.Stream == "" {

		return "", fmt.Errorf("resolve failed: empty stream path")

	}

	streamPath := result.Stream

	if strings.HasPrefix(streamPath, "http://") || strings.HasPrefix(streamPath, "https://") {

		return streamPath, nil

	}

	if !strings.HasPrefix(streamPath, "/") {

		streamPath = "/" + streamPath

	}

	return c.baseURL + streamPath, nil

}

// ResolveChannel resolves HLS for a catalog channel using its daddyId.
func (c *TVClient) ResolveChannel(channel Channel) (*StreamInfo, error) {

	hls, err := c.ResolveHLS(channel.DaddyID)

	if err != nil {
		return nil, err
	}

	return &StreamInfo{Channel: channel, HLSURL: hls}, nil

}

func (c *TVClient) cachedCatalog() *ChannelCatalog {

	c.catalogMu.RLock()
	defer c.catalogMu.RUnlock()

	if c.catalog == nil || time.Since(c.catalogAt) > catalogTTL {

		return nil

	}

	copy := *c.catalog

	return &copy

}

func (c *TVClient) storeCatalog(catalog *ChannelCatalog) {

	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()

	c.catalog = catalog
	c.catalogAt = time.Now()

}

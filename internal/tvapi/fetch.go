package tvapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed data/tv-channels.json
var embeddedCatalogJSON []byte

const catalogRefreshTimeout = 45 * time.Second

type ntvChannelsResponse struct {
	Success  bool         `json:"success"`
	Channels []ntvChannel `json:"channels"`
}

type ntvChannel struct {
	ChannelID    string `json:"channel_id"`
	ChannelName  string `json:"channel_name"`
	ChannelCode  string `json:"channel_code"`
	ChannelImage string `json:"channel_image"`
	ChannelURL   string `json:"channel_url"`
	Server       string `json:"server"`
}

func seedEmbeddedCatalog(c *TVClient) {

	catalog, err := embeddedCatalog()

	if err != nil {

		log.Printf("[tvapi] embedded catalog unavailable: %v", err)
		return

	}

	c.storeCatalog(catalog)

}

func (c *TVClient) ListChannels() (*ChannelCatalog, error) {

	if cached := c.anyCatalog(); cached != nil {

		return cached, nil

	}

	return c.fetchCatalog(catalogRefreshTimeout)

}

func (c *TVClient) Warmup() {

	if c.anyCatalog() == nil {

		seedEmbeddedCatalog(c)

	}

	c.refreshOnce.Do(func() {

		go c.runCatalogRefreshLoop()

	})

	c.enrichOnce.Do(func() {

		go c.runEnrichmentLoop()

	})

}

func (c *TVClient) runCatalogRefreshLoop() {

	c.refreshCatalog()

	ticker := time.NewTicker(catalogTTL)
	defer ticker.Stop()

	for range ticker.C {

		c.refreshCatalog()

	}

}

func (c *TVClient) refreshCatalog() {

	if _, err := c.fetchCatalog(catalogRefreshTimeout); err != nil {

		log.Printf("[tvapi] catalog refresh failed: %v", err)
		return

	}

	c.enrichCurrentCatalog()

}

func (c *TVClient) fetchCatalog(timeout time.Duration) (*ChannelCatalog, error) {

	client := &http.Client{Timeout: timeout}

	catalog, err := fetchNtvChannels(client, c.baseURL)

	if err != nil {

		return nil, err

	}

	c.storeCatalog(catalog)

	log.Printf("[tv] catalog refreshed from ntv (%d channels)", len(catalog.Channels))

	return catalog, nil

}

func fetchNtvChannels(client *http.Client, baseURL string) (*ChannelCatalog, error) {

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if baseURL == "" {

		baseURL = defaultTVBaseURL

	}

	pageURL := baseURL + "/api/get-channels"

	request, err := http.NewRequest(http.MethodGet, pageURL, nil)

	if err != nil {

		return nil, fmt.Errorf("ntv channels: build request: %w", err)

	}

	request.Header.Set("User-Agent", tvBrowserUA)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")

	response, err := client.Do(request)

	if err != nil {

		return nil, fmt.Errorf("ntv channels: fetch: %w", err)

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		return nil, fmt.Errorf("ntv channels: status %d", response.StatusCode)

	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 12<<20))

	if err != nil {

		return nil, fmt.Errorf("ntv channels: read: %w", err)

	}

	var payload ntvChannelsResponse

	if err := json.Unmarshal(body, &payload); err != nil {

		return nil, fmt.Errorf("ntv channels: decode: %w", err)

	}

	if !payload.Success && len(payload.Channels) == 0 {

		return nil, fmt.Errorf("ntv channels: empty response")

	}

	seen := make(map[string]struct{}, len(payload.Channels))
	channels := make([]Channel, 0, len(payload.Channels))

	for _, raw := range payload.Channels {

		if !strings.EqualFold(strings.TrimSpace(raw.Server), "cdnlive") {

			continue

		}

		id := strings.TrimSpace(raw.ChannelID)
		name := strings.TrimSpace(raw.ChannelName)

		if id == "" || name == "" {

			continue

		}

		if _, dup := seen[id]; dup {

			continue

		}

		seen[id] = struct{}{}

		code := strings.ToLower(strings.TrimSpace(raw.ChannelCode))
		image := strings.TrimSpace(raw.ChannelImage)

		channels = append(channels, Channel{

			ID:   id,
			Name: name,
			Slug: makeChannelSlug(name),

			Logo:  image,
			Image: image,

			Country: Country{

				Code: code,
				Name: strings.ToUpper(code),
			},

			ChannelURL: strings.TrimSpace(raw.ChannelURL),
			Source:     "cdnlive",
		})

	}

	if len(channels) == 0 {

		return nil, fmt.Errorf("ntv channels: no cdnlive channels found")

	}

	return &ChannelCatalog{

		Source:   "ntv",
		Total:    len(channels),
		Channels: channels,
	}, nil

}

func makeChannelSlug(name string) string {

	name = strings.ToLower(name)

	var b strings.Builder

	prev := '-'

	for _, r := range name {

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {

			b.WriteRune(r)
			prev = r

		} else if prev != '-' {

			b.WriteByte('-')
			prev = '-'

		}

	}

	return strings.Trim(b.String(), "-")

}

func titleCase(s string) string {

	words := strings.Fields(s)

	for i, w := range words {

		if w == "" {

			continue

		}

		// Short tokens are typically country codes or abbreviations (USA, CNN, HD).
		if n := utf8.RuneCountInString(w); n == 2 || n == 3 {

			words[i] = strings.ToUpper(w)
			continue

		}

		words[i] = strings.ToUpper(w[:1]) + w[1:]

	}

	return strings.Join(words, " ")

}

func embeddedCatalog() (*ChannelCatalog, error) {

	var catalog ChannelCatalog

	if err := json.Unmarshal(embeddedCatalogJSON, &catalog); err != nil {

		return nil, err

	}

	if len(catalog.Channels) == 0 {

		return nil, fmt.Errorf("embedded catalog is empty")

	}

	for index := range catalog.Channels {

		ch := &catalog.Channels[index]

		if ch.Logo == "" && ch.Image != "" {

			ch.Logo = ch.Image

		}

	}

	return &catalog, nil

}

func (c *TVClient) anyCatalog() *ChannelCatalog {

	c.catalogMu.RLock()
	defer c.catalogMu.RUnlock()

	if c.catalog == nil {

		return nil

	}

	copy := *c.catalog

	return &copy

}

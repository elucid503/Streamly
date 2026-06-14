package tvapi

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed data/tv-channels.json
var embeddedCatalogJSON []byte

const catalogRefreshTimeout = 45 * time.Second

type catalogProvider struct {

	name string

	url string

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

	return nil, fmt.Errorf("catalog unavailable")

}

func (c *TVClient) Warmup() {

	if c.anyCatalog() == nil {

		seedEmbeddedCatalog(c)

	}

	c.refreshOnce.Do(func() {

		go c.runCatalogRefreshLoop()

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

	}

}

func (c *TVClient) fetchCatalog(timeout time.Duration) (*ChannelCatalog, error) {

	client := &http.Client{Timeout: timeout}

	var errs []error

	for _, provider := range c.catalogProviders() {

		catalog, err := c.fetchFromProvider(client, provider)

		if err == nil {

			c.storeCatalog(catalog)

			log.Printf("[tvapi] catalog refreshed from %s (%d channels)", provider.name, len(catalog.Channels))

			return catalog, nil

		}

		log.Printf("[tvapi] catalog provider %s failed: %v", provider.name, err)

		errs = append(errs, fmt.Errorf("%s: %w", provider.name, err))

	}

	return nil, errors.Join(errs...)

}

func (c *TVClient) fetchFromProvider(client *http.Client, provider catalogProvider) (*ChannelCatalog, error) {

	request, err := http.NewRequest(http.MethodGet, provider.url, nil)

	if err != nil {

		return nil, err

	}

	referer := providerReferer(provider.url)

	request.Header.Set("User-Agent", tvBrowserUA)
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("Referer", referer)

	request.Header.Set("Accept-Encoding", "identity")

	response, err := client.Do(request)

	if err != nil {

		return nil, err

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))

		return nil, fmt.Errorf("status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))

	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))

	if err != nil {

		return nil, err

	}

	return parseCatalogJSON(body)

}

func (c *TVClient) catalogProviders() []catalogProvider {

	seen := make(map[string]struct{})

	providers := make([]catalogProvider, 0, 4)

	add := func(name, rawURL string) {

		rawURL = strings.TrimSpace(rawURL)

		if rawURL == "" {

			return

		}

		url := normalizeCatalogURL(rawURL)

		if _, ok := seen[url]; ok {

			return

		}

		seen[url] = struct{}{}

		if name == "" {

			name = url

		}

		providers = append(providers, catalogProvider{

			name: name,

			url: url,

		})

	}

	for index, rawURL := range catalogURLCandidates() {

		add(fmt.Sprintf("catalog-%d", index+1), rawURL)

	}

	return providers

}

func catalogURLCandidates() []string {

	if urls := strings.TrimSpace(os.Getenv("TV_CATALOG_URLS")); urls != "" {

		parts := strings.Split(urls, ",")
		candidates := make([]string, 0, len(parts))

		for _, part := range parts {

			if trimmed := strings.TrimSpace(part); trimmed != "" {

				candidates = append(candidates, trimmed)

			}

		}

		if len(candidates) > 0 {

			return candidates // explicitly provided URLs take precedence

		}

	}

	if base := strings.TrimSpace(os.Getenv("TV_BASE_URL")); base != "" {

		return []string{base} // a base URL can be used as a fallback
	}

	return []string{""} // nothing provided, will rely on embedded catalog

}

func normalizeCatalogURL(rawURL string) string {

	rawURL = strings.TrimSpace(rawURL)

	if strings.Contains(rawURL, "tv-channels.json") {

		return rawURL

	}

	return strings.TrimRight(rawURL, "/") + channelsPath

}

func providerReferer(rawURL string) string {

	if strings.Contains(rawURL, "://") {

		parts := strings.SplitN(strings.TrimPrefix(rawURL, "https://"), "/", 2)

		if len(parts) > 0 && parts[0] != "" {

			return "https://" + parts[0] + "/"

		}

	}

	base := os.Getenv("TV_BASE_URL")

	if base != "" {

		return normalizeCatalogURL(base)

	}

	return base + "/"

}

func parseCatalogJSON(body []byte) (*ChannelCatalog, error) {

	var catalog ChannelCatalog

	if err := json.Unmarshal(body, &catalog); err != nil {

		return nil, fmt.Errorf("decode channels: %w", err)

	}

	if len(catalog.Channels) == 0 {

		return nil, fmt.Errorf("decode channels: empty catalog")

	}

	return &catalog, nil

}

func embeddedCatalog() (*ChannelCatalog, error) {

	return parseCatalogJSON(embeddedCatalogJSON)

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

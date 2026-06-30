package tvapi

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed data/tv-channels.json
var embeddedCatalogJSON []byte

const (
	dlhdDefaultBaseURL    = "https://dlhd.pk"
	catalogRefreshTimeout = 45 * time.Second
)

var channelCardPattern = regexp.MustCompile(`href="/watch\.php\?id=(\d+)"\s+data-title="([^"]+)"`)

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

	catalog, err := scrapeDLHDChannels(client)

	if err != nil {

		return nil, err

	}

	c.storeCatalog(catalog)

	log.Printf("[tv] catalog refreshed from dlhd.pk (%d channels)", len(catalog.Channels))

	return catalog, nil

}

func scrapeDLHDChannels(client *http.Client) (*ChannelCatalog, error) {

	base := strings.TrimRight(strings.TrimSpace(os.Getenv("TV_DLHD_BASE_URL")), "/")

	if base == "" {

		base = dlhdDefaultBaseURL

	}

	pageURL := base + "/24-7-channels.php"

	request, err := http.NewRequest(http.MethodGet, pageURL, nil)

	if err != nil {

		return nil, fmt.Errorf("dlhd scrape: build request: %w", err)

	}

	request.Header.Set("User-Agent", tvBrowserUA)
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	request.Header.Set("Referer", base+"/")

	response, err := client.Do(request)

	if err != nil {

		return nil, fmt.Errorf("dlhd scrape: fetch: %w", err)

	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {

		return nil, fmt.Errorf("dlhd scrape: status %d", response.StatusCode)

	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))

	if err != nil {

		return nil, fmt.Errorf("dlhd scrape: read: %w", err)

	}

	matches := channelCardPattern.FindAllSubmatch(body, -1)

	seen := make(map[string]struct{}, len(matches))
	channels := make([]Channel, 0, len(matches))

	for _, m := range matches {

		daddyID := string(m[1])

		if _, dup := seen[daddyID]; dup {

			continue

		}

		seen[daddyID] = struct{}{}

		name := html.UnescapeString(string(m[2]))
		slug := makeChannelSlug(name)

		channels = append(channels, Channel{

			ID:      "dl-" + daddyID,
			DaddyID: daddyID,
			Name:    titleCase(name),
			Slug:    slug,
			Logo:    base + "/logos/" + makeLogoSlug(name) + ".png",
			Source:  "tv247",

		})

	}

	if len(channels) == 0 {

		return nil, fmt.Errorf("dlhd scrape: no channels found in page")

	}

	return &ChannelCatalog{

		Source:   "tv247",
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

func makeLogoSlug(name string) string {

	name = strings.ToLower(name)

	var b strings.Builder

	prev := '_'

	for _, r := range name {

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {

			b.WriteRune(r)
			prev = r

		} else if prev != '_' {

			b.WriteByte('_')
			prev = '_'

		}

	}

	return strings.Trim(b.String(), "_")

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

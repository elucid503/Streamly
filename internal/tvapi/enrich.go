package tvapi

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultChannelMetadataURL = "https://iptv-org.github.io/api/channels.json"
	defaultChannelLogosURL    = "https://iptv-org.github.io/api/logos.json"

	defaultEnrichmentInterval = 5 * time.Minute
	enrichmentTimeout         = 45 * time.Second
)

type iptvChannel struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	AltNames   []string `json:"alt_names"`
	Country    string   `json:"country"`
	Categories []string `json:"categories"`
	IsNSFW     bool     `json:"is_nsfw"`
	Closed     string   `json:"closed"`
}

type iptvLogo struct {
	Channel string `json:"channel"`
	InUse   bool   `json:"in_use"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Format  string `json:"format"`
	URL     string `json:"url"`
}

type channelMetadataIndex struct {
	byKey map[string][]channelMetadata
}

type channelMetadata struct {
	ID         string
	Name       string
	Country    string
	Categories []string
	Logo       string
}

func (c *TVClient) runEnrichmentLoop() {

	c.refreshEnrichment()

	ticker := time.NewTicker(enrichmentInterval())
	defer ticker.Stop()

	for range ticker.C {

		c.refreshEnrichment()

	}

}

func (c *TVClient) refreshEnrichment() {

	client := &http.Client{Timeout: enrichmentTimeout}

	index, err := fetchChannelMetadataIndex(client)

	if err != nil {

		log.Printf("[tvapi] enrichment refresh failed: %v", err)
		return

	}

	c.metadataMu.Lock()
	c.metadata = index
	c.metadataMu.Unlock()

	count := c.enrichCurrentCatalog()

	log.Printf("[tvapi] enrichment refreshed (%d matched channels)", count)

}

func (c *TVClient) enrichCurrentCatalog() int {

	c.metadataMu.RLock()
	index := c.metadata
	c.metadataMu.RUnlock()

	if index == nil {

		return 0

	}

	c.catalogMu.Lock()
	defer c.catalogMu.Unlock()

	if c.catalog == nil {

		return 0

	}

	next := cloneChannelCatalog(c.catalog)
	count := enrichChannelCatalog(next, index)

	c.catalog = next

	return count

}

func fetchChannelMetadataIndex(client *http.Client) (*channelMetadataIndex, error) {

	channels, err := fetchIPTVChannels(client)

	if err != nil {

		return nil, err

	}

	logos, err := fetchIPTVLogos(client)

	if err != nil {

		return nil, err

	}

	return buildChannelMetadataIndex(channels, logos), nil

}

func fetchIPTVChannels(client *http.Client) ([]iptvChannel, error) {

	body, err := fetchEnrichmentJSON(client, channelMetadataURL())

	if err != nil {

		return nil, fmt.Errorf("channels: %w", err)

	}

	var channels []iptvChannel

	if err := json.Unmarshal(body, &channels); err != nil {

		return nil, fmt.Errorf("channels: decode: %w", err)

	}

	return channels, nil

}

func fetchIPTVLogos(client *http.Client) ([]iptvLogo, error) {

	body, err := fetchEnrichmentJSON(client, channelLogosURL())

	if err != nil {

		return nil, fmt.Errorf("logos: %w", err)

	}

	var logos []iptvLogo

	if err := json.Unmarshal(body, &logos); err != nil {

		return nil, fmt.Errorf("logos: decode: %w", err)

	}

	return logos, nil

}

func fetchEnrichmentJSON(client *http.Client, rawURL string) ([]byte, error) {

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)

	if err != nil {

		return nil, err

	}

	req.Header.Set("User-Agent", tvBrowserUA)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)

	if err != nil {

		return nil, err

	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {

		return nil, fmt.Errorf("status %d", resp.StatusCode)

	}

	return io.ReadAll(io.LimitReader(resp.Body, 24<<20))

}

func buildChannelMetadataIndex(channels []iptvChannel, logos []iptvLogo) *channelMetadataIndex {

	logoByChannel := bestLogosByChannel(logos)

	index := &channelMetadataIndex{

		byKey: make(map[string][]channelMetadata),
	}

	for _, channel := range channels {

		if channel.IsNSFW || channel.Closed != "" {

			continue

		}

		meta := channelMetadata{

			ID:         channel.ID,
			Name:       strings.TrimSpace(channel.Name),
			Country:    strings.ToLower(strings.TrimSpace(channel.Country)),
			Categories: channel.Categories,
			Logo:       logoByChannel[channel.ID],
		}

		if meta.ID == "" || meta.Name == "" {

			continue

		}

		for _, key := range metadataKeys(channel) {

			if key == "" {

				continue

			}

			index.byKey[key] = append(index.byKey[key], meta)

		}

	}

	return index

}

func bestLogosByChannel(logos []iptvLogo) map[string]string {

	type scored struct {
		url   string
		score int
	}

	best := make(map[string]scored)

	for _, logo := range logos {

		logo.Channel = strings.TrimSpace(logo.Channel)
		logo.URL = strings.TrimSpace(logo.URL)

		if logo.Channel == "" || logo.URL == "" {

			continue

		}

		score := logo.Width * logo.Height

		if logo.InUse {

			score += 10_000_000

		}

		switch strings.ToLower(strings.TrimSpace(logo.Format)) {

		case "png":

			score += 500_000

		case "svg":

			score += 400_000

		}

		if current, ok := best[logo.Channel]; !ok || score > current.score {

			best[logo.Channel] = scored{url: logo.URL, score: score}

		}

	}

	out := make(map[string]string, len(best))

	for k, v := range best {

		out[k] = v.url

	}

	return out

}

func metadataKeys(channel iptvChannel) []string {

	values := []string{channel.Name}
	values = append(values, channel.AltNames...)

	var keys []string

	for _, value := range values {

		for _, key := range candidateKeys(value) {

			keys = appendUnique(keys, key)

		}

	}

	return keys

}

func enrichChannelCatalog(catalog *ChannelCatalog, index *channelMetadataIndex) int {

	if catalog == nil || index == nil {

		return 0

	}

	count := 0

	for i := range catalog.Channels {

		match, ok := index.match(catalog.Channels[i].Name)

		if !ok {

			continue

		}

		catalog.Channels[i].Name = match.Name
		catalog.Channels[i].Slug = makeChannelSlug(match.Name)

		if match.Logo != "" {

			catalog.Channels[i].Logo = match.Logo

		}

		if match.Country != "" {

			catalog.Channels[i].Country = Country{

				Code: match.Country,
				Name: strings.ToUpper(match.Country),
			}

		}

		if category := displayCategory(match.Categories); category != "" {

			catalog.Channels[i].Category = category

		}

		catalog.Channels[i].Enriched = true
		count++

	}

	return count

}

func (index *channelMetadataIndex) match(name string) (channelMetadata, bool) {

	keys, countryHint := matchKeys(name)

	var best channelMetadata
	bestScore := -1

	for keyIndex, key := range keys {

		for _, candidate := range index.byKey[key] {

			score := 100 - keyIndex

			if countryHint != "" && candidate.Country == countryHint {

				score += 60

			} else if countryHint != "" && candidate.Country != "" {

				score -= 30

			} else if candidate.Country == "us" {

				score += 8

			}

			if candidate.Logo != "" {

				score += 10

			}

			if len(candidate.Categories) > 0 {

				score += 5

			}

			if score > bestScore {

				best = candidate
				bestScore = score

			}

		}

	}

	return best, bestScore >= 100

}

func matchKeys(name string) ([]string, string) {

	name = strings.TrimSpace(name)
	stripped, countryHint := stripCountrySuffix(name)

	keys := candidateKeys(stripped)

	if countryHint != "" {

		keys = append(keys, candidateKeys(removeTrailingRegionToken(stripped))...)

	}

	keys = append(keys, candidateKeys(name)...)

	return uniqueNonEmpty(keys), countryHint

}

func candidateKeys(name string) []string {

	name = strings.TrimSpace(name)

	if name == "" {

		return nil

	}

	withoutCountry, _ := stripCountrySuffix(name)
	withoutParen, paren := splitParenthetical(withoutCountry)

	keys := []string{

		normalizeChannelKey(withoutCountry),
		normalizeChannelKey(withoutParen),
		normalizeChannelKey(paren),
	}

	if expanded, ok := channelAliases[normalizeChannelKey(withoutCountry)]; ok {

		keys = append(keys, normalizeChannelKey(expanded))

	}

	if expanded, ok := channelAliases[normalizeChannelKey(withoutParen)]; ok {

		keys = append(keys, normalizeChannelKey(expanded))

	}

	return uniqueNonEmpty(keys)

}

func normalizeChannelKey(value string) string {

	value = strings.ToLower(htmlEntityReplacer.Replace(value))
	value = strings.ReplaceAll(value, "+", "plus")

	var b strings.Builder

	for _, r := range value {

		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {

			b.WriteRune(r)

		}

	}

	return b.String()

}

func splitParenthetical(value string) (string, string) {

	start := strings.Index(value, "(")
	end := strings.LastIndex(value, ")")

	if start < 0 || end <= start {

		return value, ""

	}

	return strings.TrimSpace(value[:start]), strings.TrimSpace(value[start+1 : end])

}

func stripCountrySuffix(value string) (string, string) {

	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))

	if len(fields) == 0 {

		return value, ""

	}

	for size := 2; size >= 1; size-- {

		if len(fields) < size {

			continue

		}

		suffix := strings.Join(fields[len(fields)-size:], " ")

		if code, ok := countrySuffixes[suffix]; ok {

			kept := strings.Fields(value)[:len(fields)-size]

			return strings.TrimSpace(strings.Join(kept, " ")), code

		}

	}

	return value, ""

}

func removeTrailingRegionToken(value string) string {

	fields := strings.Fields(value)

	if len(fields) <= 1 {

		return value

	}

	last := strings.ToLower(strings.Trim(fields[len(fields)-1], ".,"))

	if _, ok := usRegionTokens[last]; ok {

		return strings.Join(fields[:len(fields)-1], " ")

	}

	return value

}

func displayCategory(categories []string) string {

	for _, category := range categories {

		category = strings.TrimSpace(category)

		if category == "" || category == "xxx" {

			continue

		}

		words := strings.Fields(strings.ReplaceAll(category, "-", " "))

		for i, word := range words {

			words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])

		}

		return strings.Join(words, " ")

	}

	return ""

}

func cloneChannelCatalog(catalog *ChannelCatalog) *ChannelCatalog {

	if catalog == nil {

		return nil

	}

	copy := *catalog
	copy.Channels = append([]Channel(nil), catalog.Channels...)

	return &copy

}

func appendUnique(items []string, item string) []string {

	if item == "" {

		return items

	}

	for _, existing := range items {

		if existing == item {

			return items

		}

	}

	return append(items, item)

}

func uniqueNonEmpty(items []string) []string {

	out := make([]string, 0, len(items))

	for _, item := range items {

		out = appendUnique(out, item)

	}

	return out

}

func channelMetadataURL() string {

	if rawURL := strings.TrimSpace(os.Getenv("TV_CHANNEL_METADATA_URL")); rawURL != "" {

		return rawURL

	}

	return defaultChannelMetadataURL

}

func channelLogosURL() string {

	if rawURL := strings.TrimSpace(os.Getenv("TV_CHANNEL_LOGOS_URL")); rawURL != "" {

		return rawURL

	}

	return defaultChannelLogosURL

}

func enrichmentInterval() time.Duration {

	if raw := strings.TrimSpace(os.Getenv("TV_CHANNEL_ENRICH_INTERVAL")); raw != "" {

		if d, err := time.ParseDuration(raw); err == nil && d > 0 {

			return d

		}

	}

	return defaultEnrichmentInterval

}

var htmlEntityReplacer = strings.NewReplacer(

	"&amp;", "&",
	"&#038;", "&",
	"&#38;", "&",
	"&plus;", "+",
)

var channelAliases = map[string]string{

	"ahc": "American Heroes Channel",
	"btn": "Big Ten Network",
	"fs1": "Fox Sports 1",
	"fs2": "Fox Sports 2",
	"yes": "YES Network",
}

var countrySuffixes = map[string]string{

	"argentina":      "ar",
	"australia":      "au",
	"brazil":         "br",
	"canada":         "ca",
	"croatia":        "hr",
	"de":             "de",
	"france":         "fr",
	"germany":        "de",
	"india":          "in",
	"ireland":        "ie",
	"italy":          "it",
	"malaysia":       "my",
	"netherlands":    "nl",
	"new zealand":    "nz",
	"nz":             "nz",
	"pk":             "pk",
	"poland":         "pl",
	"portugal":       "pt",
	"serbia":         "rs",
	"spain":          "es",
	"uae":            "ae",
	"uk":             "uk",
	"united kingdom": "uk",
	"united states":  "us",
	"us":             "us",
	"usa":            "us",
}

var usRegionTokens = map[string]struct{}{

	"atlanta":      {},
	"boston":       {},
	"chicago":      {},
	"dallas":       {},
	"denver":       {},
	"detroit":      {},
	"houston":      {},
	"la":           {},
	"los angeles":  {},
	"miami":        {},
	"ny":           {},
	"orlando":      {},
	"philadelphia": {},
	"phoenix":      {},
	"seattle":      {},
	"sf":           {},
	"tampa":        {},
	"washington":   {},
}

package febapi

import (
	"bytes"
	"crypto/cipher"
	"crypto/des"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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

// Endpoint and request-signing constants for the Showbox mobile API.
var showbox = struct {
	baseURL string
	appKey  string
	iv      string
	key     string
}{
	baseURL: "https://mbpapi.shegu.net/api/api_client/index/",
	appKey:  "moviebox",
	iv:      "wEiphTn!",
	key:     "123d6cedf626dy54233aa1w6",
}

// Static parameters baked into every encrypted request payload.
var baseParams = struct {
	appVersion string
	lang       string
	platform   string
	channel    string
	appID      string
	version    string
	medium     string
}{
	appVersion: "11.5",
	lang:       "en",
	platform:   "android",
	channel:    "Website",
	appID:      "27",
	version:    "129",
	medium:     "Website",
}

const requestTTLSeconds = 60 * 60 * 12 // How long a signed request stays valid, in seconds (currently 12 hours).

const searchCacheTTL = 60 * time.Minute

type searchCacheEntry struct {
	results []SearchResult
	expires time.Time
}

// ShowboxOptions tunes a ShowboxClient instance.
type ShowboxOptions struct {
	ChildMode string // Showbox "child mode" flag. Defaults to CHILD_MODE env or "0".
}

// ShowboxClient talks to the Showbox catalogue: search, title details, and resolving the Febbox share that hosts the media.
type ShowboxClient struct {
	childMode   string
	client      *http.Client
	searchMu    sync.RWMutex
	searchCache map[string]searchCacheEntry
}

// NewShowboxClient builds a ShowboxClient with optional overrides.
func NewShowboxClient(options ShowboxOptions) *ShowboxClient {

	childMode := options.ChildMode

	if childMode == "" {
		childMode = os.Getenv("CHILD_MODE")
	}

	if childMode == "" {
		childMode = "0"
	}

	return &ShowboxClient{
		childMode:   childMode,
		client:      &http.Client{},
		searchCache: make(map[string]searchCacheEntry),
	}

}

// TopHot returns trending search keywords from Showbox.
func (c *ShowboxClient) TopHot(mediaType MediaType, pageLimit int) ([]string, error) {

	if mediaType != MediaMovie && mediaType != MediaTV {
		mediaType = MediaMovie
	}

	if pageLimit == 0 {
		pageLimit = 25
	}

	var keywords []string

	err := c.request("Search_hot", map[string]any{
		"type":      mediaType,
		"pagelimit": pageLimit,
	}, &keywords)

	return keywords, err

}

// TopLists returns curated ranking categories for movies or TV shows.
func (c *ShowboxClient) TopLists(boxType BoxType) ([]TopList, error) {

	var lists []TopList

	err := c.request("Top_list", map[string]any{"box_type": boxType}, &lists)

	return lists, err

}

// TopListMovies returns titles from a curated movie ranking.
func (c *ShowboxClient) TopListMovies(listID string, page, pageLimit int) ([]SearchResult, error) {

	if page == 0 {
		page = 1
	}

	if pageLimit == 0 {
		pageLimit = 20
	}

	var results []SearchResult

	err := c.request("Top_list_movie", map[string]any{
		"id":        listID,
		"page":      page,
		"pagelimit": pageLimit,
	}, &results)

	return results, err

}

// TopListTV returns titles from a curated TV ranking.
func (c *ShowboxClient) TopListTV(listID string, page, pageLimit int) ([]SearchResult, error) {

	if page == 0 {
		page = 1
	}

	if pageLimit == 0 {
		pageLimit = 20
	}

	var results []SearchResult

	err := c.request("Top_list_tv", map[string]any{
		"id":        listID,
		"page":      page,
		"pagelimit": pageLimit,
	}, &results)

	return results, err

}

func searchCacheKey(query string, mediaType MediaType, page, pageLimit int) string {

	return fmt.Sprintf("%s|%s|%d|%d", strings.ToLower(strings.TrimSpace(query)), mediaType, page, pageLimit)

}

// Search queries the catalogue for movies and/or shows matching query.
func (c *ShowboxClient) Search(query string, mediaType MediaType, page, pageLimit int) ([]SearchResult, error) {

	if mediaType == "" {
		mediaType = MediaAll
	}

	if page == 0 {
		page = 1
	}

	if pageLimit == 0 {
		pageLimit = 20
	}

	key := searchCacheKey(query, mediaType, page, pageLimit)

	c.searchMu.RLock()

	if entry, ok := c.searchCache[key]; ok && time.Now().Before(entry.expires) {
		c.searchMu.RUnlock()
		return append([]SearchResult(nil), entry.results...), nil
	}

	c.searchMu.RUnlock()

	var results []SearchResult

	err := c.request("Search5", map[string]any{
		"keyword":   query,
		"type":      mediaType,
		"page":      page,
		"pagelimit": pageLimit,
	}, &results)

	if err != nil {
		return nil, err
	}

	c.searchMu.Lock()
	c.searchCache[key] = searchCacheEntry{
		results: append([]SearchResult(nil), results...),
		expires: time.Now().Add(searchCacheTTL),
	}
	c.searchMu.Unlock()

	return results, nil

}

// GetMovie fetches full details for a movie by its Showbox id.
func (c *ShowboxClient) GetMovie(movieID int) (map[string]any, error) {

	var data map[string]any

	err := c.request("Movie_detail", map[string]any{"mid": movieID}, &data)

	return data, err

}

// GetShow fetches full details for a TV series by its Showbox id.
func (c *ShowboxClient) GetShow(showID int) (map[string]any, error) {

	var data map[string]any

	err := c.request("TV_detail_v2", map[string]any{"tid": showID}, &data)

	return data, err

}

// GetEpisodeList fetches the list of episodes for a TV series season, returning a map of episode number to title.
// Returns an empty map (not an error) when the API provides no episode-level data for that season.
func (c *ShowboxClient) GetEpisodeList(showID, season int) (map[int]string, error) {

	var data map[string]any

	err := c.request("TV_detail_v2", map[string]any{"tid": showID, "season": season}, &data)

	if err != nil {
		return nil, err
	}

	episodes, _ := data["episode"].([]any)

	titles := make(map[int]string, len(episodes))

	for _, item := range episodes {

		ep, ok := item.(map[string]any)

		if !ok {
			continue
		}

		epSeason, _ := ep["season"].(float64)

		if int(epSeason) != season {
			continue
		}

		num, _ := ep["episode"].(float64)
		title := strings.TrimSpace(fmt.Sprint(ep["title"]))

		if num > 0 && title != "" && title != "<nil>" {
			titles[int(num)] = title
		}

	}

	return titles, nil

}

// GetFebBoxID resolves the Febbox share key that hosts a title's files. Pass the id and box_type from a SearchResult.
func (c *ShowboxClient) GetFebBoxID(id int, boxType BoxType) (string, error) {

	endpoint := fmt.Sprintf("https://www.showbox.media/index/share_link?id=%d&type=%d", id, boxType)

	response, err := c.client.Get(endpoint)

	if err != nil {
		return "", err
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)

	if err != nil {
		return "", err
	}

	var parsed struct {
		Data *struct {
			Link string `json:"link"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", err
	}

	if parsed.Data == nil || parsed.Data.Link == "" {
		return "", nil
	}

	parts := strings.Split(parsed.Data.Link, "/")

	return parts[len(parts)-1], nil

}

// encrypt TripleDES-encrypts a request body with the Showbox key and IV.
func encrypt(payload string) (string, error) {

	key := []byte(showbox.key)
	iv := []byte(showbox.iv)

	block, err := des.NewTripleDESCipher(key)

	if err != nil {
		return "", err
	}

	padded := pkcs7Pad([]byte(payload), block.BlockSize())

	ciphertext := make([]byte, len(padded))

	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)

	return base64.StdEncoding.EncodeToString(ciphertext), nil

}

// sign builds the tamper-check hash the server uses to validate a request.
func sign(encrypted string) string {

	hashedKey := md5Hex(showbox.appKey)

	return md5Hex(hashedKey + showbox.key + encrypted)

}

// request signs and POSTs to a Showbox module, unmarshaling its data field into dest.
func (c *ShowboxClient) request(module string, params map[string]any, dest any) error {

	requestData := map[string]any{
		"childmode": c.childMode,

		"APP_VERSION": baseParams.appVersion,
		"LANG":        baseParams.lang,

		"PLATFORM": baseParams.platform,
		"CHANNEL":  baseParams.channel,

		"APPID":   baseParams.appID,
		"VERSION": baseParams.version,

		"MEDIUM": baseParams.medium,

		"expired_date": time.Now().Unix() + requestTTLSeconds,

		"module": module,
	}

	for key, value := range params {
		requestData[key] = value
	}

	payload, err := json.Marshal(requestData)

	if err != nil {
		return err
	}

	encrypted, err := encrypt(string(payload))

	if err != nil {
		return err
	}

	envelope, err := json.Marshal(map[string]string{
		"app_key":      md5Hex(showbox.appKey),
		"verify":       sign(encrypted),
		"encrypt_data": encrypted,
	})

	if err != nil {
		return err
	}

	form := url.Values{
		"data":     {base64.StdEncoding.EncodeToString(envelope)},
		"appid":    {baseParams.appID},
		"platform": {baseParams.platform},
		"version":  {baseParams.version},
		"medium":   {baseParams.medium},
	}

	body := form.Encode() + "&token" + randomHex(32) // A trailing random token keeps the request from being cache-collapsed.

	request, err := http.NewRequest(http.MethodPost, showbox.baseURL, strings.NewReader(body))

	if err != nil {
		return err
	}

	request.Header.Set("Platform", baseParams.platform)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("User-Agent", "okhttp/3.2.0")

	response, err := c.client.Do(request)

	if err != nil {
		return err
	}

	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)

	if err != nil {
		return err
	}

	var wrapper struct {
		Data json.RawMessage `json:"data"`
	}

	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return err
	}

	if dest == nil {
		return nil
	}

	return json.Unmarshal(wrapper.Data, dest)

}

// randomHex returns a handful of bytes of random hex, used as a per-request cache buster.
func randomHex(length int) string {

	bytes := make([]byte, length/2)

	_, _ = rand.Read(bytes)

	return hex.EncodeToString(bytes)

}

func md5Hex(value string) string {

	sum := md5.Sum([]byte(value))

	return hex.EncodeToString(sum[:])

}

func pkcs7Pad(data []byte, blockSize int) []byte {

	padding := blockSize - len(data)%blockSize

	padText := bytes.Repeat([]byte{byte(padding)}, padding)

	return append(data, padText...)

}

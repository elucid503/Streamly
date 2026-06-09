package febapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const febboxBaseURL = "https://www.febbox.com" // Base URL for the Febbox share/console endpoints.

// A desktop Chrome UA. Febbox is picky about non-browser clients.
const browserUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"

// FebboxOptions tunes a FebboxClient instance.
type FebboxOptions struct {
	Cookie string // The `ui` auth cookie. Defaults to the FEBBOX_UI_COOKIE env var.
}

// FebboxClient browses Febbox shares: folder trees and per-file download qualities.
type FebboxClient struct {
	cookie string
	client *http.Client
}

// NewFebboxClient builds a FebboxClient with optional overrides.
func NewFebboxClient(options FebboxOptions) *FebboxClient {

	cookie := options.Cookie

	if cookie == "" {
		cookie = os.Getenv("FEBBOX_UI_COOKIE")
	}

	return &FebboxClient{
		cookie: cookie,
		client: &http.Client{},
	}

}

// ListFiles lists the entries of a shared folder.
func (c *FebboxClient) ListFiles(shareKey string, parentID any, cookie string) ([]FebboxFile, error) {

	url := fmt.Sprintf("%s/file/file_share_list?share_key=%s&pwd=&parent_id=%v&is_html=0", febboxBaseURL, shareKey, parentID)

	var data struct {
		Data struct {
			FileList []FebboxFile `json:"file_list"`
		} `json:"data"`
	}

	if err := c.fetchJSON(url, shareKey, cookie, &data); err != nil {
		return nil, err
	}

	return data.Data.FileList, nil

}

// GetLinks resolves the available download qualities for a single video file. Requires cookie.
func (c *FebboxClient) GetLinks(shareKey string, fid any, cookie string) ([]FileQuality, error) {

	if err := c.requireCookie(cookie); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/console/video_quality_list?fid=%v", febboxBaseURL, fid)

	var data struct {
		HTML string `json:"html"`
	}

	if err := c.fetchJSON(url, shareKey, cookie, &data); err != nil {
		return nil, err
	}

	return parseQualities(data.HTML), nil

}

// requireCookie fails fast when an auth-only endpoint is reached without a cookie.
func (c *FebboxClient) requireCookie(cookie string) error {

	auth := cookie

	if auth == "" {
		auth = c.cookie
	}

	if auth != "" {
		return nil
	}

	return fmt.Errorf("Febbox auth cookie is required; set FEBBOX_UI_COOKIE or pass { cookie }")

}

// headers presents us as a browser tab on the relevant share page.
func (c *FebboxClient) headers(shareKey, cookie string) map[string]string {

	headers := map[string]string{
		"user-agent":      browserUA,
		"accept-language": "en-US,en;q=0.9",
	}

	auth := cookie

	if auth == "" {
		auth = c.cookie
	}

	if auth != "" {
		headers["cookie"] = "ui=" + auth
	}

	if shareKey != "" {
		headers["referer"] = febboxBaseURL + "/share/" + shareKey
	}

	return headers

}

// fetchJSON fetches and parses a JSON endpoint, returning an error on a non-2xx response.
func (c *FebboxClient) fetchJSON(url, shareKey, cookie string, dest any) error {

	request, err := http.NewRequest(http.MethodGet, url, nil)

	if err != nil {
		return err
	}

	for key, value := range c.headers(shareKey, cookie) {
		request.Header.Set(key, value)
	}

	response, err := c.client.Do(request)

	if err != nil {
		return err
	}

	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Error fetching %s: %s", url, response.Status)
	}

	body, err := io.ReadAll(response.Body)

	if err != nil {
		return err
	}

	return json.Unmarshal(body, dest)

}

var (
	fileQualityOpenRe = regexp.MustCompile(`(?is)<([a-z][a-z0-9]*)[^>]*\bfile_quality\b[^>]*>`)
	attrValueRe       = regexp.MustCompile(`(?i)([a-z0-9_-]+)\s*=\s*"([^"]*)"`)

	speedSpanRe = regexp.MustCompile(`(?is)<[^>]*\bclass\s*=\s*"[^"]*\bspeed\b[^"]*"[^>]*>.*?<span[^>]*>(.*?)</span>`)
	tagStripRe  = regexp.MustCompile(`(?is)<[^>]+>`)
)

// parseQualities extracts { url, quality, name, speed, size } records from the quality-list HTML fragment.
func parseQualities(html string) []FileQuality {

	matches := fileQualityOpenRe.FindAllStringSubmatchIndex(html, -1)

	qualities := make([]FileQuality, 0, len(matches))

	for _, match := range matches {

		if len(match) < 4 {
			continue
		}

		openTag := html[match[0]:match[1]]
		tagName := html[match[2]:match[3]]
		contentStart := match[1]

		block, ok := innerHTMLUntilCloseTag(html, contentStart, tagName)

		if !ok {
			continue
		}

		qualities = append(qualities, FileQuality{
			URL:     extractAttr(openTag, "data-url"),
			Quality: extractAttr(openTag, "data-quality"),
			Name:    extractClassText(block, "name"),
			Speed:   extractSpeed(block),
			Size:    extractClassText(block, "size"),
		})

	}

	return qualities

}

// innerHTMLUntilCloseTag returns the slice before the first case-insensitive </tagName>.
func innerHTMLUntilCloseTag(html string, contentStart int, tagName string) (string, bool) {

	closeTag := "</" + strings.ToLower(tagName) + ">"
	lower := strings.ToLower(html[contentStart:])
	end := strings.Index(lower, closeTag)

	if end < 0 {
		return "", false
	}

	return html[contentStart : contentStart+end], true

}

func extractAttr(tag, name string) string {

	for _, match := range attrValueRe.FindAllStringSubmatch(tag, -1) {

		if len(match) < 3 {
			continue
		}

		if strings.EqualFold(match[1], name) {
			return match[2]
		}

	}

	return ""

}

func extractClassText(block, className string) string {

	pattern := fmt.Sprintf(`(?is)<[^>]*\bclass\s*=\s*"[^"]*\b%s\b[^"]*"[^>]*>(.*?)</[^>]+>`, regexp.QuoteMeta(className))
	match := regexp.MustCompile(pattern).FindStringSubmatch(block)

	if len(match) < 3 {
		return ""
	}

	return strings.TrimSpace(stripTags(match[2]))

}

func extractSpeed(block string) string {

	match := speedSpanRe.FindStringSubmatch(block)

	if len(match) < 2 {
		return ""
	}

	return strings.TrimSpace(stripTags(match[1]))

}

func stripTags(value string) string {

	return tagStripRe.ReplaceAllString(value, "")

}
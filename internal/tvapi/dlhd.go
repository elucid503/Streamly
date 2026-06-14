package tvapi

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const defaultDLHDBaseURL = "https://dlhd.pk"

var (
	iframeSrcPattern = regexp.MustCompile(`iframe src="([^"]+)"`)
	atobSourcePattern = regexp.MustCompile(`source:\s*window\.atob\('([^']+)'\)`)
)

func (c *TVClient) resolveDLHD(daddyID string) (ResolvedStream, error) {

	base := strings.TrimRight(dlhdBaseURL(), "/")

	streamPage := fmt.Sprintf("%s/stream/stream-%s.php", base, daddyID)

	response, err := c.get(streamPage, base+"/")

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("fetch dlhd stream page: %w", err)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("read dlhd stream page: %w", err)
	}

	if response.StatusCode != http.StatusOK {

		return ResolvedStream{}, fmt.Errorf("fetch dlhd stream page: status %d", response.StatusCode)
	}

	embedURL, ok := extractIframeSrc(string(body))

	if !ok {

		return ResolvedStream{}, fmt.Errorf("dlhd stream page missing embed iframe")
	}

	embedURL = normalizeURL(embedURL, base)

	embedResponse, err := c.get(embedURL, streamPage)

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("fetch dlhd embed: %w", err)
	}

	defer embedResponse.Body.Close()

	embedBody, err := io.ReadAll(io.LimitReader(embedResponse.Body, 2<<20))

	if err != nil {

		return ResolvedStream{}, fmt.Errorf("read dlhd embed: %w", err)
	}

	if embedResponse.StatusCode != http.StatusOK {

		return ResolvedStream{}, fmt.Errorf("fetch dlhd embed: status %d", embedResponse.StatusCode)
	}

	playlistURL, ok := extractAtobSource(string(embedBody))

	if !ok {

		return ResolvedStream{}, fmt.Errorf("dlhd embed missing playlist source")
	}

	return ResolvedStream{

		URL: playlistURL,

		Referer: embedURL,
	}, nil

}

func dlhdBaseURL() string {

	if base := strings.TrimSpace(os.Getenv("TV_DLHD_BASE_URL")); base != "" {

		return strings.TrimRight(base, "/")
	}

	return defaultDLHDBaseURL

}

func extractIframeSrc(page string) (string, bool) {

	match := iframeSrcPattern.FindStringSubmatch(page)

	if len(match) < 2 {

		return "", false
	}

	src := strings.TrimSpace(match[1])

	if src == "" {

		return "", false
	}

	return src, true

}

func extractAtobSource(page string) (string, bool) {

	match := atobSourcePattern.FindStringSubmatch(page)

	if len(match) < 2 {

		return "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(match[1])

	if err != nil {

		return "", false
	}

	playlistURL := strings.TrimSpace(string(decoded))

	if playlistURL == "" {

		return "", false
	}

	return playlistURL, true

}

func normalizeURL(raw, base string) string {

	raw = strings.TrimSpace(raw)

	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {

		return raw
	}

	if strings.HasPrefix(raw, "//") {

		return "https:" + raw
	}

	if strings.HasPrefix(raw, "/") {

		return strings.TrimRight(base, "/") + raw
	}

	return raw

}

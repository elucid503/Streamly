package tvapi

import (
	"bytes"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"time"
)

// ChannelThumb downloads the logo at logoURL and returns it as a transparent PNG.
func (c *TVClient) ChannelThumb(logoURL string) ([]byte, error) {

	if logoURL == "" {

		return nil, nil

	}

	client := &http.Client{Timeout: 8 * time.Second}

	req, err := http.NewRequest(http.MethodGet, logoURL, nil)

	if err != nil {

		return nil, err

	}

	req.Header.Set("User-Agent", tvBrowserUA)

	resp, err := client.Do(req)

	if err != nil {

		return nil, err

	}

	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if err != nil {

		return nil, err

	}

	img, _, err := image.Decode(bytes.NewReader(data))

	if err != nil {

		return nil, err

	}

	var buf bytes.Buffer

	if err := png.Encode(&buf, img); err != nil {

		return nil, err

	}

	return buf.Bytes(), nil

}
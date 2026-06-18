package tvapi

import (
	"encoding/json"
	"fmt"
	"strings"
)

type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`

	Flag string `json:"flag"`
}

func (country *Country) UnmarshalJSON(data []byte) error {

	var object struct {
		Code string `json:"code"`
		Name string `json:"name"`

		Flag string `json:"flag"`
	}

	if err := json.Unmarshal(data, &object); err == nil {

		country.Code = strings.ToLower(strings.TrimSpace(object.Code))
		country.Name = strings.TrimSpace(object.Name)
		country.Flag = strings.TrimSpace(object.Flag)

		return nil

	}

	var value string

	if err := json.Unmarshal(data, &value); err != nil {

		return fmt.Errorf("decode country: %w", err)

	}

	value = strings.TrimSpace(value)

	country.Code = strings.ToLower(value)
	country.Name = value
	country.Flag = ""

	return nil

}

type Channel struct {
	ID      string `json:"id"`
	DaddyID string `json:"daddyId"`

	Name string `json:"name"`
	Slug string `json:"slug"`

	Logo string `json:"logo"`
	Image string `json:"image"`

	Country  Country `json:"country"`
	Category string  `json:"category"`

	Status string `json:"status"`

	Source string `json:"source"`
}

type ChannelCatalog struct {
	Generated string `json:"generated"`

	Total  int    `json:"total"`
	Source string `json:"source"`

	StreamAPI string `json:"streamApi"`

	Channels []Channel `json:"channels"`
}

type ResolveResult struct {
	Success bool   `json:"success"`
	Stream  string `json:"stream"`

	Error string `json:"error"`
}

type TV247ResolveResult struct {
	ChannelID string `json:"channelId"`

	ProxyPlaylistURL string `json:"proxyPlaylistUrl"`
	ProxyPlayerURL   string `json:"proxyPlayerUrl"`

	Error string `json:"error"`
}

type ResolvedStream struct {
	URL string

	Referer string
}

type StreamInfo struct {
	Channel Channel

	HLSURL string
}

package tvapi

type Country struct {

	Code string `json:"code"`
	Name string `json:"name"`

	Flag string `json:"flag"`
}

type Channel struct {

	ID string `json:"id"`
	DaddyID string `json:"daddyId"`

	Name string `json:"name"`
	Slug string `json:"slug"`

	Logo string `json:"logo"`

	Country Country `json:"country"`
	Category string `json:"category"`

	Status string `json:"status"`

	Source string `json:"source"`
}

type ChannelCatalog struct {

	Generated string `json:"generated"`

	Total int `json:"total"`
	Source string `json:"source"`

	StreamAPI string `json:"streamApi"`

	Channels []Channel `json:"channels"`
}

type ResolveResult struct {

	Success bool `json:"success"`
	Stream string `json:"stream"`

	Error string `json:"error"`
}

type TV247ResolveResult struct {

	ChannelID string `json:"channelId"`

	ProxyPlaylistURL string `json:"proxyPlaylistUrl"`
	ProxyPlayerURL string `json:"proxyPlayerUrl"`

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

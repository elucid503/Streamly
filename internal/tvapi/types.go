package tvapi

// Country holds channel country metadata from tv-channels.json.
type Country struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Flag string `json:"flag"`
}

// Channel is a single Live TV entry from the catalog.
type Channel struct {
	ID      string `json:"id"`
	DaddyID string `json:"daddyId"`

	Name string `json:"name"`
	Slug string `json:"slug"`

	Logo string `json:"logo"`

	Country  Country `json:"country"`
	Category string  `json:"category"`

	Status string `json:"status"`

	Source string `json:"source"`
}

// ChannelCatalog is the top-level response from tv-channels.json.
type ChannelCatalog struct {
	Generated string `json:"generated"`

	Total  int    `json:"total"`
	Source string `json:"source"`

	StreamAPI string `json:"streamApi"`

	Channels []Channel `json:"channels"`
}

// ResolveResult is the legacy dami-tv.pro resolve payload.
type ResolveResult struct {
	Success bool   `json:"success"`
	Stream  string `json:"stream"`

	Error string `json:"error"`
}

// TV247ResolveResult is returned by the tv247 resolve-dlstream API.
type TV247ResolveResult struct {
	ChannelID        string `json:"channelId"`
	ProxyPlaylistURL string `json:"proxyPlaylistUrl"`
	ProxyPlayerURL   string `json:"proxyPlayerUrl"`

	Error string `json:"error"`
}

// ResolvedStream is a live TV playlist URL and the Referer it expects.
type ResolvedStream struct {
	URL     string
	Referer string
}

// StreamInfo pairs a channel with its resolved HLS playlist URL.
type StreamInfo struct {
	Channel Channel
	HLSURL  string
}

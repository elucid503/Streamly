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

// ResolveResult is returned by /papi/tv/resolve/{daddyId}.
type ResolveResult struct {
	Success bool   `json:"success"`
	Stream  string `json:"stream"`

	Error string `json:"error"`
}

// StreamInfo pairs a channel with its resolved HLS playlist URL.
type StreamInfo struct {
	Channel Channel
	HLSURL  string
}

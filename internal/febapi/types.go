package febapi

// MediaType is the kind of catalogue entry Showbox search understands.
type MediaType string

const (
	MediaAll   MediaType = "all"
	MediaMovie MediaType = "movie"
	MediaTV    MediaType = "tv"
)

// BoxType is Showbox's discriminator between a movie and a series.
type BoxType int

const (
	BoxMovie  BoxType = 1
	BoxSeries BoxType = 2
)

// SearchResult is a single hit from a Showbox search or autocomplete query.
type SearchResult struct {
	ID      int     `json:"id"`
	BoxType BoxType `json:"box_type"` // Tells ShowboxClient.GetFebBoxID which detail endpoint to resolve.
	Title   string  `json:"title"`

	Year        int    `json:"year,omitempty"`
	Poster      string `json:"poster,omitempty"`
	Description string `json:"description,omitempty"`
	IMDBRating  string `json:"imdb_rating,omitempty"`
}

// TopList is a curated Showbox ranking category (e.g. "Popular on Netflix").
type TopList struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// FebboxFile is an entry in a Febbox shared folder; either a file or a sub-folder.
type FebboxFile struct {
	FID      int    `json:"fid"` // The id used to list children or resolve a file's qualities.
	FileName string `json:"file_name"`
	IsDir    int    `json:"is_dir"` // 1 for a directory (e.g. a season), 0 for a playable file.
}

// FileQuality is one downloadable rendition of a Febbox video.
type FileQuality struct {
	URL     string `json:"url"` // Direct media URL; may point at MP4 or an HLS playlist.
	Quality string `json:"quality"`
	Speed   string `json:"speed"`
	Size    string `json:"size"`
	Name    string `json:"name"`
}
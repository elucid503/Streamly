package febapi

type MediaType string

const (

	MediaAll MediaType = "all"
	MediaMovie MediaType = "movie"
	MediaTV MediaType = "tv"

)

type BoxType int

const (

	BoxMovie BoxType = 1
	BoxSeries BoxType = 2

)

type SearchResult struct {

	ID int `json:"id"`
	BoxType BoxType `json:"box_type"`
	Title string `json:"title"`

	Year int `json:"year,omitempty"`
	Poster string `json:"poster,omitempty"`
	Description string `json:"description,omitempty"`

	IMDBRating string `json:"imdb_rating,omitempty"`

}

type TopList struct {

	ID string `json:"id"`

	DisplayName string `json:"display_name"`

}

type FebboxFile struct {

	FID int `json:"fid"`
	FileName string `json:"file_name"`

	IsDir int `json:"is_dir"`

}

type FileQuality struct {

	URL string `json:"url"`
	Quality string `json:"quality"`

	Speed string `json:"speed"`
	Size string `json:"size"`

	Name string `json:"name"`

}

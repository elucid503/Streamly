package media

import (
	"sort"

	"streamly/internal/febapi"
	"streamly/internal/source"
)

// RankedQualityURLs returns progressive Febbox URLs to try, best fit first then fallbacks.
func RankedQualityURLs(qualities []febapi.FileQuality, target int) []string {

	progressive := make([]febapi.FileQuality, 0, len(qualities))

	for _, quality := range qualities {
		if quality.URL != "" && !source.IsHlsURL(quality.URL) {
			progressive = append(progressive, quality)
		}
	}

	if len(progressive) == 0 {
		return nil
	}

	sort.SliceStable(progressive, func(i, j int) bool {

		hi := QualityHeight(progressive[i])
		hj := QualityHeight(progressive[j])

		if hi != hj {
			return hi < hj
		}

		return StreamFilePreference(progressive[i].Name) > StreamFilePreference(progressive[j].Name)

	})

	ordered := make([]febapi.FileQuality, 0, len(progressive))

	for _, quality := range progressive {
		if QualityHeight(quality) >= target {
			ordered = append(ordered, quality)
		}
	}

	for i := len(progressive) - 1; i >= 0; i-- {
		if QualityHeight(progressive[i]) < target {
			ordered = append(ordered, progressive[i])
		}
	}

	seen := make(map[string]struct{}, len(ordered))
	urls := make([]string, 0, len(ordered))

	for _, quality := range ordered {
		if _, ok := seen[quality.URL]; ok {
			continue
		}

		seen[quality.URL] = struct{}{}
		urls = append(urls, quality.URL)
	}

	return urls

}

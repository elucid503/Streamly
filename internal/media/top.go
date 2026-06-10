package media

import (
	"fmt"
	"strings"

	"streamly/internal/febapi"
)

const TopLimit = 10

// Top returns up to limit trending titles resolved from Showbox hot searches.
func (r *Resolver) Top(limit int) ([]febapi.SearchResult, error) {

	if limit <= 0 {
		limit = TopLimit
	}

	keywords, err := r.showbox.TopHot(febapi.MediaMovie, limit*3)

	if err != nil {
		return nil, err
	}

	var matches []febapi.SearchResult
	seen := make(map[string]struct{})

	for _, keyword := range keywords {

		if len(matches) >= limit {
			break
		}

		keyword = strings.TrimSpace(keyword)

		if keyword == "" {
			continue
		}

		results, err := r.showbox.Search(keyword, febapi.MediaAll, 1, 1)

		if err != nil || len(results) == 0 {
			continue
		}

		hit := results[0]
		key := fmt.Sprintf("%d:%d", hit.BoxType, hit.ID)

		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}
		matches = append(matches, hit)

	}

	return matches, nil

}

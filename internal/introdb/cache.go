package introdb

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

const introCacheTTL = 6 * time.Hour

type cacheEntry struct {

	record *MediaRecord

	expiresAt time.Time

}

type CachedClient struct {

	inner *Client

	mu sync.RWMutex
	cache map[string]cacheEntry

}

func NewCachedClient(client *Client) *CachedClient {

	return &CachedClient{

		inner: client,
		cache: make(map[string]cacheEntry),

	}

}

func (c *CachedClient) GetMedia(query MediaQuery) (*MediaRecord, error) {

	key := cacheKey(query)

	c.mu.RLock()

	entry, ok := c.cache[key]

	c.mu.RUnlock()

	if ok && time.Now().Before(entry.expiresAt) {

		return entry.record, nil
	}

	record, err := c.inner.GetMedia(query)

	if err != nil {

		return nil, err
	}

	c.mu.Lock()

	c.cache[key] = cacheEntry{

		record: record,
		expiresAt: time.Now().Add(introCacheTTL),
	}

	c.mu.Unlock()

	return record, nil

}

func cacheKey(query MediaQuery) string {

	key := fmt.Sprintf("tmdb:%d:imdb:%s", query.TMDBId, query.IMDBId)

	if query.Season > 0 && query.Episode > 0 {

		key += fmt.Sprintf(":s%de%d", query.Season, query.Episode)
	}

	if query.DurationMs > 0 {

		key += ":d" + strconv.FormatInt(query.DurationMs, 10)
	}

	return key

}

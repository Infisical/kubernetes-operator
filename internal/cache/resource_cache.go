package cache

import (
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

type ResourceCache struct {
	cache *ristretto.Cache[string, string]
	ttl   time.Duration
}

func NewResourceCache(ttl time.Duration) (*ResourceCache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config[string, string]{
		NumCounters:        1000,
		MaxCost:            1 << 20,
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create resource cache: %w", err)
	}
	return &ResourceCache{cache: cache, ttl: ttl}, nil
}

func (c *ResourceCache) Get(key string) (string, bool) {
	return c.cache.Get(key)
}

func (c *ResourceCache) Set(key string, value string) {
	c.cache.SetWithTTL(key, value, 1, c.ttl)
	c.cache.Wait()
}

func (c *ResourceCache) Cleanup() {
	if c.cache != nil {
		c.cache.Close()
	}
}

func ProjectBySlugCacheKey(slug string) string {
	return fmt.Sprintf("project_by_slug_%s", slug)
}

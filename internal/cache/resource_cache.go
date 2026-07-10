package cache

import (
	"fmt"
	"time"

	"github.com/dgraph-io/ristretto/v2"
)

type ResourceCache[V any] struct {
	cache *ristretto.Cache[string, V]
	ttl   time.Duration
}

func NewResourceCache[V any](ttl time.Duration) (*ResourceCache[V], error) {
	cache, err := ristretto.NewCache(&ristretto.Config[string, V]{
		NumCounters:        1000,
		MaxCost:            1 << 20,
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create resource cache: %w", err)
	}
	return &ResourceCache[V]{cache: cache, ttl: ttl}, nil
}

func (c *ResourceCache[V]) Get(key string) (V, bool) {
	return c.cache.Get(key)
}

func (c *ResourceCache[V]) Set(key string, value V) {
	c.cache.SetWithTTL(key, value, 1, c.ttl)
	c.cache.Wait()
}

func (c *ResourceCache[V]) Cleanup() {
	if c.cache != nil {
		c.cache.Close()
	}
}

func ProjectBySlugCacheKey(authNamespace, authName, slug string) string {
	return fmt.Sprintf("project_by_slug_%s/%s/%s", authNamespace, authName, slug)
}

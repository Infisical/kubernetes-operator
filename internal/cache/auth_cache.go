package cache

import (
	"time"

	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/jellydator/ttlcache/v3"
)

type ClientCacheKey struct {
	Name      string
	Namespace string
	Method    string
}

type AuthCache struct {
	cache *ttlcache.Cache[ClientCacheKey, *model.AuthenticationResult]
}

func NewAuthCache() *AuthCache {
	c := ttlcache.New[ClientCacheKey, *model.AuthenticationResult]()
	go c.Start()
	return &AuthCache{cache: c}
}

func (c *AuthCache) Get(key ClientCacheKey) (*model.AuthenticationResult, bool) {
	item := c.cache.Get(key)
	if item == nil {
		return nil, false
	}
	return item.Value(), true
}

func (c *AuthCache) Set(key ClientCacheKey, value *model.AuthenticationResult, ttl time.Duration) {
	c.cache.Set(key, value, ttl)
}

func (c *AuthCache) Delete(key ClientCacheKey) {
	c.cache.Delete(key)
}

func (c *AuthCache) Cleanup() {
	c.cache.Stop()
	c.cache.DeleteAll()
}

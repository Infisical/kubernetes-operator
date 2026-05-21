package cache

import (
	"fmt"
	"time"

	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/dgraph-io/ristretto/v2"
)

type ClientCacheKey struct {
	Name      string
	Namespace string
}

func (k ClientCacheKey) String() string {
	return fmt.Sprintf("%s/%s", k.Namespace, k.Name)
}

type AuthCache struct {
	cache            *ristretto.Cache[string, *model.AuthenticationResult]
	minTTLThreashold time.Duration
}

type AuthCacheOption func(*AuthCache)

func WithMinTTLThreshold(minTTL time.Duration) AuthCacheOption {
	return func(ac *AuthCache) {
		ac.minTTLThreashold = minTTL
	}
}

func NewAuthCache(opts ...AuthCacheOption) (*AuthCache, error) {
	cache, err := ristretto.NewCache(&ristretto.Config[string, *model.AuthenticationResult]{
		NumCounters:        1000,
		MaxCost:            1 << 30,
		BufferItems:        64,
		IgnoreInternalCost: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}
	ac := &AuthCache{cache: cache}
	for _, opt := range opts {
		opt(ac)
	}
	return ac, nil
}

func (c *AuthCache) Get(key ClientCacheKey) (*model.AuthenticationResult, bool) {
	return c.cache.Get(key.String())
}

func (c *AuthCache) Set(key ClientCacheKey, value *model.AuthenticationResult, ttl time.Duration) {
	if ttl >= c.minTTLThreashold {
		c.cache.SetWithTTL(key.String(), value, 1, ttl)
		c.cache.Wait()
	}
}

func (c *AuthCache) Delete(key ClientCacheKey) {
	c.cache.Del(key.String())
}

func (c *AuthCache) Cleanup() {
	if c.cache != nil {
		c.cache.Close()
	}
}

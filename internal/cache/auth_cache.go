package cache

import (
	"fmt"
	"sync"
	"time"

	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/dgraph-io/badger/v4"
)

type ClientCacheKey struct {
	Name      string
	Namespace string
}

func (k ClientCacheKey) String() string {
	return fmt.Sprintf("%s/%s", k.Namespace, k.Name)
}

type AuthCache struct {
	db     *badger.DB
	values sync.Map
}

func NewAuthCache() *AuthCache {
	opts := badger.DefaultOptions("").WithInMemory(true).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		panic(fmt.Sprintf("failed to open badger: %v", err))
	}
	return &AuthCache{db: db}
}

func (c *AuthCache) Get(key ClientCacheKey) (*model.AuthenticationResult, bool) {
	k := key.String()

	err := c.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get([]byte(k))
		return err
	})
	if err != nil {
		c.values.Delete(k)
		return nil, false
	}

	v, ok := c.values.Load(k)
	if !ok {
		return nil, false
	}
	return v.(*model.AuthenticationResult), true
}

func (c *AuthCache) Set(key ClientCacheKey, value *model.AuthenticationResult, ttl time.Duration) {
	k := key.String()
	c.db.Update(func(txn *badger.Txn) error {
		e := badger.NewEntry([]byte(k), []byte{1})
		if ttl > 0 {
			e = e.WithTTL(ttl)
		}
		return txn.SetEntry(e)
	})
	c.values.Store(k, value)
}

func (c *AuthCache) Delete(key ClientCacheKey) {
	k := key.String()
	c.db.Update(func(txn *badger.Txn) error {
		return txn.Delete([]byte(k))
	})
	c.values.Delete(k)
}

func (c *AuthCache) Cleanup() {
	c.db.Close()
}

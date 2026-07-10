package cache_test

import (
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/cache"
)

var _ = Describe("ResourceCache", func() {
	var resourceCache *cache.ResourceCache

	AfterEach(func() {
		if resourceCache != nil {
			resourceCache.Cleanup()
		}
	})

	Describe("basic operations", func() {
		BeforeEach(func() {
			var err error
			resourceCache, err = cache.NewResourceCache(30 * time.Second)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should store and retrieve a value", func() {
			resourceCache.Set("key-1", "value-1")

			result, found := resourceCache.Get("key-1")
			Expect(found).To(BeTrue())
			Expect(result).To(Equal("value-1"))
		})

		It("should return false for a key that was never set", func() {
			_, found := resourceCache.Get("nonexistent")
			Expect(found).To(BeFalse())
		})

		It("should overwrite an existing key", func() {
			resourceCache.Set("key-1", "original")
			resourceCache.Set("key-1", "updated")

			result, found := resourceCache.Get("key-1")
			Expect(found).To(BeTrue())
			Expect(result).To(Equal("updated"))
		})

		It("should store multiple distinct keys", func() {
			resourceCache.Set("key-a", "value-a")
			resourceCache.Set("key-b", "value-b")
			resourceCache.Set("key-c", "value-c")

			for _, pair := range []struct{ k, v string }{
				{"key-a", "value-a"},
				{"key-b", "value-b"},
				{"key-c", "value-c"},
			} {
				result, found := resourceCache.Get(pair.k)
				Expect(found).To(BeTrue())
				Expect(result).To(Equal(pair.v))
			}
		})
	})

	Describe("TTL expiration", func() {
		BeforeEach(func() {
			var err error
			resourceCache, err = cache.NewResourceCache(2 * time.Second)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not find a key after its TTL expires", func() {
			resourceCache.Set("ephemeral", "gone-soon")

			result, found := resourceCache.Get("ephemeral")
			Expect(found).To(BeTrue())
			Expect(result).To(Equal("gone-soon"))

			time.Sleep(3 * time.Second)

			_, found = resourceCache.Get("ephemeral")
			Expect(found).To(BeFalse())
		})
	})

	Describe("parallel access", func() {
		BeforeEach(func() {
			var err error
			resourceCache, err = cache.NewResourceCache(30 * time.Second)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should handle concurrent writes to different keys", func() {
			const goroutines = 50
			var wg sync.WaitGroup
			wg.Add(goroutines)

			for i := 0; i < goroutines; i++ {
				go func(idx int) {
					defer wg.Done()
					key := fmt.Sprintf("concurrent-key-%d", idx)
					value := fmt.Sprintf("concurrent-value-%d", idx)
					resourceCache.Set(key, value)
				}(i)
			}
			wg.Wait()

			for i := 0; i < goroutines; i++ {
				key := fmt.Sprintf("concurrent-key-%d", i)
				expected := fmt.Sprintf("concurrent-value-%d", i)
				result, found := resourceCache.Get(key)
				Expect(found).To(BeTrue())
				Expect(result).To(Equal(expected))
			}
		})

		It("should handle concurrent reads and writes to the same key", func() {
			resourceCache.Set("shared", "initial")

			const goroutines = 50
			var wg sync.WaitGroup
			wg.Add(goroutines * 2)

			for i := 0; i < goroutines; i++ {
				go func(idx int) {
					defer wg.Done()
					resourceCache.Set("shared", fmt.Sprintf("writer-%d", idx))
				}(i)

				go func() {
					defer wg.Done()
					val, found := resourceCache.Get("shared")
					if found {
						Expect(val).NotTo(BeEmpty())
					}
				}()
			}
			wg.Wait()

			result, found := resourceCache.Get("shared")
			Expect(found).To(BeTrue())
			Expect(result).To(HavePrefix("writer-"))
		})

		It("should return the correct value per key under parallel access", func() {
			keys := []string{"project-a", "project-b", "project-c"}
			values := []string{"data-a", "data-b", "data-c"}

			for i, k := range keys {
				resourceCache.Set(k, values[i])
			}

			const readersPerKey = 30
			var wg sync.WaitGroup
			wg.Add(len(keys) * readersPerKey)

			for i, k := range keys {
				expected := values[i]
				for r := 0; r < readersPerKey; r++ {
					go func(key, exp string) {
						defer wg.Done()
						result, found := resourceCache.Get(key)
						Expect(found).To(BeTrue())
						Expect(result).To(Equal(exp))
					}(k, expected)
				}
			}
			wg.Wait()
		})
	})

	Describe("cleanup", func() {
		It("should not panic when calling Cleanup on a valid cache", func() {
			var err error
			resourceCache, err = cache.NewResourceCache(10 * time.Second)
			Expect(err).NotTo(HaveOccurred())

			resourceCache.Set("before-cleanup", "value")
			Expect(func() { resourceCache.Cleanup() }).NotTo(Panic())
			resourceCache = nil
		})
	})

	Describe("ProjectBySlugCacheKey", func() {
		It("should produce a deterministic key from a slug", func() {
			key := cache.ProjectBySlugCacheKey("my-project")
			Expect(key).To(Equal("project_by_slug_my-project"))
		})

		It("should produce distinct keys for different slugs", func() {
			Expect(cache.ProjectBySlugCacheKey("a")).NotTo(Equal(cache.ProjectBySlugCacheKey("b")))
		})
	})
})

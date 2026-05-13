package v1beta1_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	infisicalSdk "github.com/infisical/go-sdk"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	controllerv1beta1 "github.com/Infisical/infisical/k8-operator/internal/controller/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
)

func newAuthCRD(name, namespace string, generation int64, method secretsv1beta1.InfisicalAuthMethod, identityID string) *secretsv1beta1.InfisicalAuth {
	return &secretsv1beta1.InfisicalAuth{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Generation: generation,
		},
		Spec: secretsv1beta1.InfisicalAuthSpec{
			Method: method,
			Kubernetes: &secretsv1beta1.KubernetesAuthConfig{
				IdentityID: identityID,
			},
		},
	}
}

func seedCache(c *cache.AuthCache, name, namespace string) {
	c.Set(cache.ClientCacheKey{
		Name:      name,
		Namespace: namespace,
	}, &model.AuthenticationResult{
		MachineIdentity: infisicalSdk.MachineIdentityCredential{
			AccessToken: "cached-token",
		},
	}, 5*time.Minute)
}

func cacheHasEntry(c *cache.AuthCache, name, namespace string) bool {
	_, found := c.Get(cache.ClientCacheKey{
		Name:      name,
		Namespace: namespace,
	})
	return found
}

var _ = Describe("SpecChangedPredicate", func() {
	const (
		authName      = "auth-1"
		authNamespace = "default"
		method        = secretsv1beta1.KubernetesAuth
	)

	var (
		authCache *cache.AuthCache
		predicate *controllerv1beta1.SpecChangedPredicate
	)

	BeforeEach(func() {
		authCache = cache.NewAuthCache()
		seedCache(authCache, authName, authNamespace)

		resolver := auth.NewAuthStrategyResolverForTesting(authCache, nil)
		predicate = &controllerv1beta1.SpecChangedPredicate{
			AuthResolver: resolver,
		}
	})

	AfterEach(func() {
		authCache.Cleanup()
	})

	It("should skip reconcile and keep cache when generation is unchanged", func() {
		result := predicate.Update(event.UpdateEvent{
			ObjectOld: newAuthCRD(authName, authNamespace, 1, method, "id-1"),
			ObjectNew: newAuthCRD(authName, authNamespace, 1, method, "id-1"),
		})

		Expect(result).To(BeFalse())
		Expect(cacheHasEntry(authCache, authName, authNamespace)).To(BeTrue())
	})

	It("should reconcile but keep cache when generation changed and spec is identical", func() {
		result := predicate.Update(event.UpdateEvent{
			ObjectOld: newAuthCRD(authName, authNamespace, 1, method, "id-1"),
			ObjectNew: newAuthCRD(authName, authNamespace, 2, method, "id-1"),
		})

		Expect(result).To(BeTrue())
		Expect(cacheHasEntry(authCache, authName, authNamespace)).To(BeTrue())
	})

	It("should reconcile and evict cache when spec changed", func() {
		result := predicate.Update(event.UpdateEvent{
			ObjectOld: newAuthCRD(authName, authNamespace, 1, method, "id-1"),
			ObjectNew: newAuthCRD(authName, authNamespace, 2, method, "id-2"),
		})

		Expect(result).To(BeTrue())
		Expect(cacheHasEntry(authCache, authName, authNamespace)).To(BeFalse())
	})
})

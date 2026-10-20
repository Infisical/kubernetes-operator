package auth_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	infisicalSdk "github.com/infisical/go-sdk"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/model"
)

type fakeAuthStrategy struct {
	callCount int
	result    *model.AuthenticationResult
}

func (f *fakeAuthStrategy) Validate(_ context.Context, _ *secretsv1beta1.InfisicalAuth) error {
	return nil
}

func (f *fakeAuthStrategy) Authenticate(_ context.Context, _ *model.InfisicalConnection, _ *secretsv1beta1.InfisicalAuth) (*model.AuthenticationResult, error) {
	f.callCount++
	return f.result, nil
}

// blockingAuthStrategy blocks every Authenticate call until release is closed,
// so concurrent callers pile up on a cache miss.
type blockingAuthStrategy struct {
	calls   atomic.Int32
	release chan struct{}
	result  *model.AuthenticationResult
}

// releaseOnCleanup returns a function that unblocks every Authenticate call. It also
// registers it, followed by wg.Wait, as a cleanup so that a failed assertion never
// leaves callers blocked.
func (b *blockingAuthStrategy) releaseOnCleanup(wg *sync.WaitGroup) func() {
	release := sync.OnceFunc(func() { close(b.release) })
	DeferCleanup(func() {
		release()
		wg.Wait()
	})
	return release
}

func (b *blockingAuthStrategy) Validate(_ context.Context, _ *secretsv1beta1.InfisicalAuth) error {
	return nil
}

func (b *blockingAuthStrategy) Authenticate(_ context.Context, _ *model.InfisicalConnection, _ *secretsv1beta1.InfisicalAuth) (*model.AuthenticationResult, error) {
	b.calls.Add(1)
	<-b.release
	return b.result, nil
}

var _ = Describe("Registry", func() {
	It("should return an error for an unsupported auth method", func() {
		authCache, err := cache.NewAuthCache()
		Expect(err).ToNot(HaveOccurred())

		registry := auth.NewAuthStrategyResolver(k8sClient, authCache, logr.New(nil), false)
		authCR := newInfisicalAuth("unsupported-method")

		err = registry.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unsupported auth method"))
	})

	It("should delegate to the correct provider via the registry", func() {
		authCache, err := cache.NewAuthCache()
		Expect(err).ToNot(HaveOccurred())

		registry := auth.NewAuthStrategyResolver(k8sClient, authCache, logr.New(nil), false)

		type registryTestCase struct {
			name   string
			method secretsv1beta1.InfisicalAuthMethod
			setup  func(authCR *secretsv1beta1.InfisicalAuth)
		}

		entries := []registryTestCase{
			{
				name:   "kubernetes",
				method: secretsv1beta1.KubernetesAuth,
				setup: func(authCR *secretsv1beta1.InfisicalAuth) {
					authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
						ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "default", Namespace: "default"},
					}
				},
			},
			{
				name:   "aws-iam",
				method: secretsv1beta1.AWSIamAuth,
				setup: func(authCR *secretsv1beta1.InfisicalAuth) {
					authCR.Spec.AWSIam = &secretsv1beta1.AWSIamAuthConfig{
						IdentityIDRef: secretsv1beta1.SecretReference{Name: "aws-id", Namespace: "default", Key: "value"},
					}
				},
			},
			{
				name:   "azure",
				method: secretsv1beta1.AzureAuth,
				setup: func(authCR *secretsv1beta1.InfisicalAuth) {
					authCR.Spec.Azure = &secretsv1beta1.AzureAuthConfig{IdentityIDRef: secretsv1beta1.SecretReference{Name: "azure-id", Namespace: "default", Key: "value"}}
				},
			},
			{
				name:   "gcp-id-token",
				method: secretsv1beta1.GCPIdTokenAuth,
				setup: func(authCR *secretsv1beta1.InfisicalAuth) {
					authCR.Spec.GCPIdToken = &secretsv1beta1.GCPIdTokenAuthConfig{IdentityIDRef: secretsv1beta1.SecretReference{Name: "gcp-id-token-id", Namespace: "default", Key: "value"}}
				},
			},
			{
				name:   "gcp-iam",
				method: secretsv1beta1.GCPIamAuth,
				setup: func(authCR *secretsv1beta1.InfisicalAuth) {
					authCR.Spec.GCPIam = &secretsv1beta1.GCPIamAuthConfig{
						IdentityIDRef:             secretsv1beta1.SecretReference{Name: "gcp-iam-id", Namespace: "default", Key: "value"},
						ServiceAccountKeyFilePath: "/path",
					}
				},
			},
		}

		for _, tc := range entries {
			By(fmt.Sprintf("validating %s via the registry", tc.name))
			authCR := newInfisicalAuth(tc.method)
			tc.setup(authCR)
			Expect(registry.Validate(ctx, authCR)).To(Succeed())
		}
	})
})

var _ = Describe("Cache behavior", func() {
	var (
		authCache *cache.AuthCache
		fake      *fakeAuthStrategy
		resolver  *auth.AuthStrategyResolver
		conn      *secretsv1beta1.InfisicalConnection
		authCR    *secretsv1beta1.InfisicalAuth
	)

	BeforeEach(func() {
		var err error
		authCache, err = cache.NewAuthCache(cache.WithMinTTLThreshold(1 * time.Second))
		Expect(err).ToNot(HaveOccurred())
		// DeferCleanup (not AfterEach) so that cleanups registered by specs, which run in
		// reverse order, release blocked callers before the cache is closed.
		DeferCleanup(authCache.Cleanup)

		fake = &fakeAuthStrategy{
			result: &model.AuthenticationResult{
				MachineIdentity: infisicalSdk.MachineIdentityCredential{
					AccessToken:       "fake-token",
					ExpiresIn:         600,
					AccessTokenMaxTTL: 300,
				},
			},
		}

		resolver = auth.NewAuthStrategyResolverForTesting(k8sClient, authCache, map[secretsv1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{
			secretsv1beta1.UniversalAuth: fake,
		})

		conn = &secretsv1beta1.InfisicalConnection{
			Spec: secretsv1beta1.InfisicalConnectionSpec{
				Address: "https://app.infisical.com",
			},
		}

		authCR = newInfisicalAuth(secretsv1beta1.UniversalAuth)
	})

	It("should call the provider on the first request and return from cache on the second", func() {
		result1, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result1.MachineIdentity.AccessToken).To(Equal("fake-token"))
		Expect(fake.callCount).To(Equal(1))

		result2, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result2.MachineIdentity.AccessToken).To(Equal("fake-token"))
		Expect(fake.callCount).To(Equal(1))
	})

	It("should call the provider again after the cache entry is evicted", func() {
		result1, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result1).NotTo(BeNil())
		Expect(fake.callCount).To(Equal(1))

		cacheKey := cache.ClientCacheKey{
			Name:      authCR.GetName(),
			Namespace: authCR.GetNamespace(),
		}
		authCache.Delete(cacheKey)

		result2, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result2).NotTo(BeNil())
		Expect(fake.callCount).To(Equal(2))
	})

	It("should call the provider again after the cache TTL expires", func() {
		fake.result.MachineIdentity.ExpiresIn = 3 // 70% = 2.1s TTL, above 1s threshold

		result1, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result1).NotTo(BeNil())
		Expect(fake.callCount).To(Equal(1))

		result2, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result2).NotTo(BeNil())
		Expect(fake.callCount).To(Equal(1))

		time.Sleep(3 * time.Second)

		result3, err := resolver.Authenticate(ctx, conn, authCR)
		Expect(err).NotTo(HaveOccurred())
		Expect(result3).NotTo(BeNil())
		Expect(fake.callCount).To(Equal(2))
	})

	It("should authenticate once when called concurrently on a cache miss", func() {
		blocking := &blockingAuthStrategy{release: make(chan struct{}), result: fake.result}
		resolver = auth.NewAuthStrategyResolverForTesting(k8sClient, authCache, map[secretsv1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{
			secretsv1beta1.UniversalAuth: blocking,
		})

		const callers = 10
		var wg sync.WaitGroup
		release := blocking.releaseOnCleanup(&wg)
		errs := make(chan error, callers)
		for range callers {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				_, err := resolver.Authenticate(ctx, conn, authCR)
				errs <- err
			}()
		}

		Eventually(blocking.calls.Load).Should(Equal(int32(1)))
		// Give the remaining callers time to reach the resolver while the first one is blocked.
		time.Sleep(100 * time.Millisecond)
		release()
		wg.Wait()
		close(errs)

		for err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(blocking.calls.Load()).To(Equal(int32(1)))
	})

	It("should not serialize logins of different auths", func() {
		blocking := &blockingAuthStrategy{release: make(chan struct{}), result: fake.result}
		resolver = auth.NewAuthStrategyResolverForTesting(k8sClient, authCache, map[secretsv1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{
			secretsv1beta1.UniversalAuth: blocking,
		})

		otherAuthCR := newInfisicalAuth(secretsv1beta1.UniversalAuth)
		otherAuthCR.Name = "other-auth"

		var wg sync.WaitGroup
		release := blocking.releaseOnCleanup(&wg)
		for _, a := range []*secretsv1beta1.InfisicalAuth{authCR, otherAuthCR} {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				_, err := resolver.Authenticate(ctx, conn, a)
				Expect(err).NotTo(HaveOccurred())
			}()
		}

		// Both logins must be in flight at the same time.
		Eventually(blocking.calls.Load).Should(Equal(int32(2)))
		release()
		wg.Wait()
	})
})

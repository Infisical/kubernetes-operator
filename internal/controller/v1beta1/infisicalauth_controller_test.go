/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1beta1_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	infisicalSdk "github.com/infisical/go-sdk"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	controllerv1beta1 "github.com/Infisical/infisical/k8-operator/internal/controller/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
)

type fakeAuthStrategy struct {
	validateErr     error
	authenticateErr error
	result          *model.AuthenticationResult
}

func (f *fakeAuthStrategy) Validate(_ context.Context, _ *secretsv1beta1.InfisicalAuth) error {
	return f.validateErr
}

func (f *fakeAuthStrategy) Authenticate(_ context.Context, _ *model.InfisicalConnection, _ *secretsv1beta1.InfisicalAuth) (*model.AuthenticationResult, error) {
	if f.authenticateErr != nil {
		return nil, f.authenticateErr
	}
	return f.result, nil
}

func successStrategy() *fakeAuthStrategy {
	return &fakeAuthStrategy{
		result: &model.AuthenticationResult{
			MachineIdentity: infisicalSdk.MachineIdentityCredential{
				AccessToken:       "fake-token",
				AccessTokenMaxTTL: 300,
			},
		},
	}
}

var _ = Describe("InfisicalAuth Controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		type testCase struct {
			name             string
			method           secretsv1beta1.InfisicalAuthMethod
			connOpts         []InfisicalConnectionOpt
			authSpec         func(*secretsv1beta1.InfisicalAuthSpec)
			strategy         auth.InfisicalAuthStrategy
			createConnection bool
			expectReady      bool
		}

		entries := []testCase{
			{
				name:             "connection-not-found",
				method:           secretsv1beta1.UniversalAuth,
				createConnection: false,
				expectReady:      false,
			},
			{
				name:   "connection-not-ready",
				method: secretsv1beta1.UniversalAuth,
				connOpts: []InfisicalConnectionOpt{
					WithAddress(""),
				},
				createConnection: true,
				expectReady:      false,
			},
			{
				name:   "validation-fails",
				method: secretsv1beta1.UniversalAuth,
				connOpts: []InfisicalConnectionOpt{
					WithAddress("https://app.infisical.com"),
				},
				strategy: &fakeAuthStrategy{
					validateErr: fmt.Errorf("missing client credentials"),
				},
				createConnection: true,
				expectReady:      false,
			},
			{
				name:   "authentication-fails",
				method: secretsv1beta1.UniversalAuth,
				connOpts: []InfisicalConnectionOpt{
					WithAddress("https://app.infisical.com"),
				},
				strategy: &fakeAuthStrategy{
					authenticateErr: fmt.Errorf("invalid credentials"),
				},
				createConnection: true,
				expectReady:      false,
			},
			{
				name:   "successful-universal-auth",
				method: secretsv1beta1.UniversalAuth,
				connOpts: []InfisicalConnectionOpt{
					WithAddress("https://app.infisical.com"),
				},
				strategy:         successStrategy(),
				createConnection: true,
				expectReady:      true,
			},
			{
				name:   "successful-kubernetes-auth",
				method: secretsv1beta1.KubernetesAuth,
				connOpts: []InfisicalConnectionOpt{
					WithAddress("https://app.infisical.com"),
				},
				strategy:         successStrategy(),
				createConnection: true,
				expectReady:      true,
			},
		}

		for _, tc := range entries {
			It(fmt.Sprintf("should handle %s", tc.name), func() {
				resourceName := fmt.Sprintf("auth-%s", tc.name)
				connName := fmt.Sprintf("conn-%s", tc.name)

				if tc.createConnection {
					connOpts := append([]InfisicalConnectionOpt{WithName(connName)}, tc.connOpts...)
					createInfisicalConnection(ctx, connOpts...)
					DeferCleanup(func() { deleteInfisicalConnection(ctx, WithName(connName)) })

					connReconciler := &controllerv1beta1.InfisicalConnectionReconciler{
						Client: k8sClient,
						Scheme: k8sClient.Scheme(),
					}
					_, _ = connReconciler.Reconcile(ctx, reconcile.Request{
						NamespacedName: types.NamespacedName{Name: connName, Namespace: "default"},
					})
				}

				authCRD := &secretsv1beta1.InfisicalAuth{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: secretsv1beta1.InfisicalAuthSpec{
						Method: tc.method,
						InfisicalConnectionRef: secretsv1beta1.NamespacedName{
							Name:      connName,
							Namespace: "default",
						},
					},
				}
				if tc.authSpec != nil {
					tc.authSpec(&authCRD.Spec)
				}

				Expect(k8sClient.Create(ctx, authCRD)).To(Succeed())
				DeferCleanup(func() {
					resource := &secretsv1beta1.InfisicalAuth{}
					if err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: "default"}, resource); err == nil {
						Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
					}
				})

				authCache, err := cache.NewAuthCache()
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() { authCache.Cleanup() })

				var resolver *auth.AuthStrategyResolver
				if tc.strategy != nil {
					resolver = auth.NewAuthStrategyResolverForTesting(authCache, map[secretsv1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{
						tc.method: tc.strategy,
					})
				} else {
					resolver = auth.NewAuthStrategyResolverForTesting(authCache, map[secretsv1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{})
				}

				reconciler := &controllerv1beta1.InfisicalAuthReconciler{
					Client:       k8sClient,
					BaseLogger:   log.Log,
					Scheme:       k8sClient.Scheme(),
					AuthResolver: resolver,
				}

				namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}
				_, err = reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})

				if tc.expectReady {
					Expect(err).NotTo(HaveOccurred())
				} else {
					Expect(err).To(HaveOccurred())
				}

				resource := &secretsv1beta1.InfisicalAuth{}
				Expect(k8sClient.Get(ctx, namespacedName, resource)).To(Succeed())

				Expect(resource.Status.Conditions).NotTo(BeEmpty())
				isReady := findCondition(resource.Status.Conditions, "secrets.infisical.com/IsReady")
				Expect(isReady).NotTo(BeNil())

				if tc.expectReady {
					Expect(isReady.Status).To(Equal(metav1.ConditionTrue))
					Expect(isReady.Message).To(Equal("InfisicalAuth is ready to be used."))

					authMethod := findCondition(resource.Status.Conditions, "secrets.infisical.com/AuthMethod")
					Expect(authMethod).NotTo(BeNil())
					Expect(authMethod.Status).To(Equal(metav1.ConditionTrue))
					Expect(authMethod.Message).To(Equal(string(tc.method)))
				} else {
					Expect(isReady.Status).To(Equal(metav1.ConditionFalse))
					Expect(isReady.Message).To(ContainSubstring("not ready to be used due to an error"))
				}
			})
		}
	})
})

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

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
				IdentityIDRef: secretsv1beta1.SecretReference{
					Name:      identityID,
					Namespace: namespace,
					Key:       "identity-id",
				},
				ServiceAccountRef: secretsv1beta1.NamespacedName{
					Name:      "sa",
					Namespace: namespace,
				},
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
		var err error
		authCache, err = cache.NewAuthCache()
		Expect(err).ToNot(HaveOccurred())
		seedCache(authCache, authName, authNamespace)

		resolver := auth.NewAuthStrategyResolverForTesting(authCache, nil)
		predicate = &controllerv1beta1.SpecChangedPredicate{
			AuthResolver: resolver,
		}
	})

	AfterEach(func() {
		if authCache != nil {
			authCache.Cleanup()
		}
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

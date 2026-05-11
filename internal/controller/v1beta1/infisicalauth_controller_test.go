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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
						InfisicalConnectionRef: secretsv1beta1.InfisicalConnectionRef{
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

				authCache := cache.NewAuthCache()
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
				_, err := reconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				resource := &secretsv1beta1.InfisicalAuth{}
				Expect(k8sClient.Get(ctx, namespacedName, resource)).To(Succeed())

				Expect(resource.Status.Conditions).NotTo(BeEmpty())
				isReady := findCondition(resource.Status.Conditions, "secrets.infisical.com/IsReady")
				Expect(isReady).NotTo(BeNil())

				if tc.expectReady {
					Expect(isReady.Status).To(Equal(metav1.ConditionTrue))
					Expect(isReady.Message).To(Equal("InfisicalConnection is ready to be used."))

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

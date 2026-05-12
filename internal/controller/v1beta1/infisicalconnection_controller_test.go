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
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/controller/v1beta1"
)

var _ = Describe("InfisicalConnection Controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		type testCase struct {
			name        string
			connOpts    []InfisicalConnectionOpt
			expectReady bool
			expectErr   bool
			envMap      map[string]string
		}

		entries := []testCase{
			{
				name:        "empty-host",
				expectReady: false,
			},
			{
				name:        "valid-host-from-env",
				expectReady: true,
				envMap: map[string]string{
					"INFISICAL_HOST_API": "https://us.infisical.com",
				},
			},
			{
				name:        "valid-host",
				connOpts:    []InfisicalConnectionOpt{WithAddress("https://app.infisical.com")},
				expectReady: true,
			},
			{
				name:        "valid-host-with-api-suffix",
				connOpts:    []InfisicalConnectionOpt{WithAddress("https://app.infisical.com/api")},
				expectReady: true,
			},
			{
				name:        "invalid-host",
				connOpts:    []InfisicalConnectionOpt{WithAddress("https://invalid.not-a-real-host.example")},
				expectReady: false,
				expectErr:   true,
			},
			{
				name: "self-sign-tls-cert",
				connOpts: []InfisicalConnectionOpt{
					WithAddress("https://app.infisical.com"),
					WithTLS("tls-ca-cert", "default", "ca.crt"),
				},
				expectReady: true,
			},
		}

		for _, tc := range entries {
			It(fmt.Sprintf("should handle %s", tc.name), func() {
				if tc.envMap != nil {
					for k, v := range tc.envMap {
						os.Setenv(k, v)
					}

					DeferCleanup(func() {
						for k := range tc.envMap {
							os.Unsetenv(k)
						}
					})
				}

				resourceName := fmt.Sprintf("conn-%s", tc.name)
				opts := append([]InfisicalConnectionOpt{WithName(resourceName)}, tc.connOpts...)
				o := defaultInfisicalConnectionOpts()
				for _, fn := range opts {
					fn(&o)
				}

				if o.Spec.TLS != nil && o.Spec.TLS.CaCertificate != nil {
					createSelfSignedCACertSecret(ctx, o.Spec.TLS.CaCertificate.SecretName, o.Spec.TLS.CaCertificate.SecretNamespace, o.Spec.TLS.CaCertificate.SecretKey)
					DeferCleanup(func() {
						deleteSecret(ctx, o.Spec.TLS.CaCertificate.SecretName, o.Spec.TLS.CaCertificate.SecretNamespace)
					})
				}

				createInfisicalConnection(ctx, opts...)
				DeferCleanup(func() { deleteInfisicalConnection(ctx, WithName(resourceName)) })

				controllerReconciler := &v1beta1.InfisicalConnectionReconciler{
					Client: k8sClient,
					Scheme: k8sClient.Scheme(),
				}

				namespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: namespacedName,
				})

				resource := &secretsv1beta1.InfisicalConnection{}
				Expect(k8sClient.Get(ctx, namespacedName, resource)).To(Succeed())
				Expect(resource.Status.Conditions).NotTo(BeEmpty())

				statusCondition := resource.Status.Conditions[0]

				if tc.expectErr {
					Expect(err).To(HaveOccurred())
				} else {
					Expect(err).NotTo(HaveOccurred())
				}

				if tc.expectReady {
					Expect(statusCondition.Status).To(Equal(metav1.ConditionTrue))
					Expect(statusCondition.Message).To(Equal("InfisicalConnection is ready to be used."))
				} else {
					Expect(statusCondition.Status).To(Equal(metav1.ConditionFalse))
					Expect(statusCondition.Message).To(HavePrefix("InfisicalConnection is not ready to be used due to an error:"))
				}
			})
		}
	})
})

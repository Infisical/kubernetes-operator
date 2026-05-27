/*
MIT License

Copyright (c) 2024 Infisical

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
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
				expectErr:   true,
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

				if tls := o.Spec.TLS; tls != nil && tls.CaCertificate != nil && tls.CaCertificate.Name != "" {
					createSelfSignedCACertSecret(ctx, o.Spec.TLS.CaCertificate.Name, o.Spec.TLS.CaCertificate.Namespace, o.Spec.TLS.CaCertificate.Key)
					DeferCleanup(func() {
						deleteSecret(ctx, o.Spec.TLS.CaCertificate.Name, o.Spec.TLS.CaCertificate.Namespace)
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

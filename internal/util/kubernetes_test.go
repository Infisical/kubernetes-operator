package util_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("ResolveTLSCaCertificate", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	Context("connection-level TLS", func() {
		It("returns empty string when TLS config is nil and no global config exists", func() {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, nil, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(BeEmpty())
		})

		It("returns empty string when CaCertificate is nil and no global config exists", func() {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
			tlsConfig := &v1beta1.TLSConfig{CaCertificate: nil}
			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, tlsConfig, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(BeEmpty())
		})

		It("resolves the CA certificate from a Kubernetes secret", func() {
			expectedCert := "-----BEGIN CERTIFICATE-----\ntest-cert-content\n-----END CERTIFICATE-----"
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tls-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"ca.crt": []byte(expectedCert),
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(secret).
				Build()

			tlsConfig := &v1beta1.TLSConfig{
				CaCertificate: &v1beta1.SecretReference{
					Name:      "tls-secret",
					Namespace: "default",
					Key:       "ca.crt",
				},
			}

			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, tlsConfig, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(Equal(expectedCert))
		})

		It("returns an error when the secret does not exist", func() {
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			tlsConfig := &v1beta1.TLSConfig{
				CaCertificate: &v1beta1.SecretReference{
					Name:      "nonexistent-secret",
					Namespace: "default",
					Key:       "ca.crt",
				},
			}

			_, err := util.ResolveTLSCaCertificate(ctx, k8sClient, tlsConfig, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to resolve TLS CA certificate"))
		})

		It("returns an error when the key does not exist in the secret", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tls-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"other-key": []byte("some-value"),
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(secret).
				Build()

			tlsConfig := &v1beta1.TLSConfig{
				CaCertificate: &v1beta1.SecretReference{
					Name:      "tls-secret",
					Namespace: "default",
					Key:       "ca.crt",
				},
			}

			_, err := util.ResolveTLSCaCertificate(ctx, k8sClient, tlsConfig, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to resolve TLS CA certificate"))
		})

		It("uses connection TLS over global config when both exist", func() {
			connectionCert := "connection-level-cert"
			globalCert := "global-level-cert"

			connectionSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "connection-tls",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"ca.crt": []byte(connectionCert),
				},
			}

			globalSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "global-tls",
					Namespace: "infisical",
				},
				Data: map[string][]byte{
					"ca.crt": []byte(globalCert),
				},
			}

			globalConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.OPERATOR_SETTINGS_CONFIGMAP_NAME,
					Namespace: constants.OPERATOR_SETTINGS_CONFIGMAP_NAMESPACE,
				},
				Data: map[string]string{
					"tls.caRef.secretName":      "global-tls",
					"tls.caRef.secretNamespace": "infisical",
					"tls.caRef.key":             "ca.crt",
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(connectionSecret, globalSecret, globalConfigMap).
				Build()

			tlsConfig := &v1beta1.TLSConfig{
				CaCertificate: &v1beta1.SecretReference{
					Name:      "connection-tls",
					Namespace: "default",
					Key:       "ca.crt",
				},
			}

			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, tlsConfig, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(Equal(connectionCert))
		})
	})

	Context("global TLS fallback", func() {
		It("falls back to global configmap TLS when connection TLS is nil", func() {
			expectedCert := "global-ca-cert"

			globalSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "global-tls-secret",
					Namespace: "infisical",
				},
				Data: map[string][]byte{
					"ca.crt": []byte(expectedCert),
				},
			}

			globalConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.OPERATOR_SETTINGS_CONFIGMAP_NAME,
					Namespace: constants.OPERATOR_SETTINGS_CONFIGMAP_NAMESPACE,
				},
				Data: map[string]string{
					"tls.caRef.secretName":      "global-tls-secret",
					"tls.caRef.secretNamespace": "infisical",
					"tls.caRef.key":             "ca.crt",
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(globalSecret, globalConfigMap).
				Build()

			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, nil, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(Equal(expectedCert))
		})

		It("skips global configmap when namespace scoped", func() {
			globalConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.OPERATOR_SETTINGS_CONFIGMAP_NAME,
					Namespace: constants.OPERATOR_SETTINGS_CONFIGMAP_NAMESPACE,
				},
				Data: map[string]string{
					"tls.caRef.secretName":      "global-tls-secret",
					"tls.caRef.secretNamespace": "infisical",
					"tls.caRef.key":             "ca.crt",
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(globalConfigMap).
				Build()

			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, nil, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(BeEmpty())
		})

		It("returns empty string when global configmap has no TLS fields", func() {
			globalConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.OPERATOR_SETTINGS_CONFIGMAP_NAME,
					Namespace: constants.OPERATOR_SETTINGS_CONFIGMAP_NAMESPACE,
				},
				Data: map[string]string{
					"hostAPI": "https://app.infisical.com/api",
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(globalConfigMap).
				Build()

			cert, err := util.ResolveTLSCaCertificate(ctx, k8sClient, nil, false)
			Expect(err).NotTo(HaveOccurred())
			Expect(cert).To(BeEmpty())
		})

		It("returns an error when global TLS fields are partially set", func() {
			globalConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.OPERATOR_SETTINGS_CONFIGMAP_NAME,
					Namespace: constants.OPERATOR_SETTINGS_CONFIGMAP_NAMESPACE,
				},
				Data: map[string]string{
					"tls.caRef.secretName": "some-secret",
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(globalConfigMap).
				Build()

			_, err := util.ResolveTLSCaCertificate(ctx, k8sClient, nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("all fields must be set"))
		})

		It("returns an error when global TLS secret does not exist", func() {
			globalConfigMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.OPERATOR_SETTINGS_CONFIGMAP_NAME,
					Namespace: constants.OPERATOR_SETTINGS_CONFIGMAP_NAMESPACE,
				},
				Data: map[string]string{
					"tls.caRef.secretName":      "nonexistent",
					"tls.caRef.secretNamespace": "infisical",
					"tls.caRef.key":             "ca.crt",
				},
			}

			k8sClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(globalConfigMap).
				Build()

			_, err := util.ResolveTLSCaCertificate(ctx, k8sClient, nil, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to resolve global TLS CA certificate"))
		})
	})
})

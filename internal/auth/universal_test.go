package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("Universal Auth", func() {
	const (
		clientIDSecretName     = "universal-client-id"
		clientSecretSecretName = "universal-client-secret"
		namespace              = "default"
		secretKey              = "value"
	)

	It("should fail when .spec.universal is nil", func() {
		provider := auth.NewUniversalAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.UniversalAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.universal is not set"))
	})

	It("should fail when the referenced secrets do not exist", func() {
		provider := auth.NewUniversalAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.UniversalAuth)
		authCR.Spec.Universal = &secretsv1beta1.UniversalAuthConfig{
			ClientIdRef:     secretsv1beta1.SecretReference{Name: clientIDSecretName, Namespace: namespace, Key: secretKey},
			ClientSecretRef: secretsv1beta1.SecretReference{Name: clientSecretSecretName, Namespace: namespace, Key: secretKey},
		}

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unable to fetch secret"))
	})

	It("should succeed when secrets exist, then fail after deletion", func() {
		By("creating the required secrets")
		createSecret(clientIDSecretName, namespace, map[string][]byte{secretKey: []byte("my-client-id")})
		createSecret(clientSecretSecretName, namespace, map[string][]byte{secretKey: []byte("my-client-secret")})

		provider := auth.NewUniversalAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.UniversalAuth)
		authCR.Spec.Universal = &secretsv1beta1.UniversalAuthConfig{
			ClientIdRef:     secretsv1beta1.SecretReference{Name: clientIDSecretName, Namespace: namespace, Key: secretKey},
			ClientSecretRef: secretsv1beta1.SecretReference{Name: clientSecretSecretName, Namespace: namespace, Key: secretKey},
		}

		By("validating — should succeed")
		Expect(provider.Validate(ctx, authCR)).To(Succeed())

		By("deleting the secrets")
		deleteSecret(clientIDSecretName, namespace)
		deleteSecret(clientSecretSecretName, namespace)

		By("validating again — should fail")
		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unable to fetch secret"))
	})

	It("should fail when the secret exists but the key is missing", func() {
		secretName := "universal-wrong-key"
		createSecret(secretName, namespace, map[string][]byte{"wrong-key": []byte("data")})
		DeferCleanup(func() { deleteSecret(secretName, namespace) })

		provider := auth.NewUniversalAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.UniversalAuth)
		authCR.Spec.Universal = &secretsv1beta1.UniversalAuthConfig{
			ClientIdRef:     secretsv1beta1.SecretReference{Name: secretName, Namespace: namespace, Key: secretKey},
			ClientSecretRef: secretsv1beta1.SecretReference{Name: secretName, Namespace: namespace, Key: secretKey},
		}

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("has no value for key"))
	})
})

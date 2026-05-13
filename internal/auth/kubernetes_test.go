package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("Kubernetes Auth", func() {
	const (
		identityIDSecretName = "k8s-identity-id"
		namespace            = "default"
		secretKey            = "value"
	)

	It("should fail when .spec.kubernetes is nil", func() {
		provider := auth.NewKubernetesAuth(k8sClient, false)
		authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.kubernetes is not set"))
	})

	Context("when autoCreateServiceAccountToken is false", func() {
		It("should fail when the identityIdRef secret does not exist", func() {
			provider := auth.NewKubernetesAuth(k8sClient, false)
			authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
			authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
				IdentityIDRef:     secretsv1beta1.SecretReference{Name: "nonexistent-secret", Namespace: namespace, Key: secretKey},
				ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "default", Namespace: namespace},
			}

			err := provider.Validate(ctx, authCR)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unable to fetch secret"))
		})

		It("should fail when the identityIdRef secret exists but the key is missing", func() {
			secretName := "k8s-wrong-key"
			createSecret(secretName, namespace, map[string][]byte{"wrong-key": []byte("data")})
			DeferCleanup(func() { deleteSecret(secretName, namespace) })

			provider := auth.NewKubernetesAuth(k8sClient, false)
			authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
			authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
				IdentityIDRef:     secretsv1beta1.SecretReference{Name: secretName, Namespace: namespace, Key: secretKey},
				ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "default", Namespace: namespace},
			}

			err := provider.Validate(ctx, authCR)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("has no value for key"))
		})

		It("should succeed when the identityIdRef secret exists with the correct key", func() {
			createSecret(identityIDSecretName, namespace, map[string][]byte{secretKey: []byte("my-identity-id")})
			DeferCleanup(func() { deleteSecret(identityIDSecretName, namespace) })

			provider := auth.NewKubernetesAuth(k8sClient, false)
			authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
			authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
				IdentityIDRef:     secretsv1beta1.SecretReference{Name: identityIDSecretName, Namespace: namespace, Key: secretKey},
				ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "default", Namespace: namespace},
			}

			Expect(provider.Validate(ctx, authCR)).To(Succeed())
		})
	})

	Context("when autoCreateServiceAccountToken is true", func() {
		It("should fail when serviceAccountRef.name is empty", func() {
			provider := auth.NewKubernetesAuth(k8sClient, false)
			authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
			authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
				AutoCreateServiceAccountToken: true,
				ServiceAccountRef:             secretsv1beta1.NamespacedName{Name: "", Namespace: namespace},
			}

			err := provider.Validate(ctx, authCR)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("serviceAccountRef.name"))
		})

		It("should fail when serviceAccountRef.namespace is empty", func() {
			provider := auth.NewKubernetesAuth(k8sClient, false)
			authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
			authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
				AutoCreateServiceAccountToken: true,
				ServiceAccountRef:             secretsv1beta1.NamespacedName{Name: "my-sa", Namespace: ""},
			}

			err := provider.Validate(ctx, authCR)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("serviceAccountRef.namespace"))
		})

		It("should succeed when serviceAccountRef name and namespace are set", func() {
			provider := auth.NewKubernetesAuth(k8sClient, false)
			authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
			authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
				AutoCreateServiceAccountToken: true,
				ServiceAccountRef:             secretsv1beta1.NamespacedName{Name: "my-sa", Namespace: namespace},
			}

			Expect(provider.Validate(ctx, authCR)).To(Succeed())
		})
	})
})

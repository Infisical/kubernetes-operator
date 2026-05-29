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

	It("should fail when serviceAccountRef.name is empty", func() {
		provider := auth.NewKubernetesAuth(k8sClient, false)
		authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
		authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
			ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "", Namespace: namespace},
		}

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("serviceAccountRef.name"))
	})

	It("should fail when serviceAccountRef.namespace is empty", func() {
		provider := auth.NewKubernetesAuth(k8sClient, false)
		authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
		authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
			ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "my-sa", Namespace: ""},
		}

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("serviceAccountRef.namespace"))
	})

	It("should succeed when serviceAccountRef name and namespace are set", func() {
		provider := auth.NewKubernetesAuth(k8sClient, false)
		authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
		authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
			ServiceAccountRef: secretsv1beta1.NamespacedName{Name: "my-sa", Namespace: namespace},
		}

		Expect(provider.Validate(ctx, authCR)).To(Succeed())
	})
})

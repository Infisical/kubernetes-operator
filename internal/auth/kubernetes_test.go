package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("Kubernetes Auth", func() {
	It("should fail when .spec.kubernetes is nil", func() {
		provider := auth.NewKubernetesAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.kubernetes is not set"))
	})

	It("should succeed when .spec.kubernetes is set", func() {
		provider := auth.NewKubernetesAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.KubernetesAuth)
		authCR.Spec.Kubernetes = &secretsv1beta1.KubernetesAuthConfig{
			IdentityID:         "identity-123",
			ServiceAccountName: "default",
		}

		Expect(provider.Validate(ctx, authCR)).To(Succeed())
	})
})

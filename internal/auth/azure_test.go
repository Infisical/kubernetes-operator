package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("Azure Auth", func() {
	It("should fail when .spec.azure is nil", func() {
		provider := auth.NewAzureAuth()
		authCR := newInfisicalAuth(secretsv1beta1.AzureAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.azure is not set"))
	})

	It("should succeed when .spec.azure is set", func() {
		provider := auth.NewAzureAuth()
		authCR := newInfisicalAuth(secretsv1beta1.AzureAuth)
		authCR.Spec.Azure = &secretsv1beta1.AzureAuthConfig{
			IdentityID: "identity-123",
		}

		Expect(provider.Validate(ctx, authCR)).To(Succeed())
	})
})

package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("GCP ID Token Auth", func() {
	It("should fail when .spec.gcp-id-token is nil", func() {
		provider := auth.NewGCPIdTokenAuth()
		authCR := newInfisicalAuth(secretsv1beta1.GCPIdTokenAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.gcp-id-token is not set"))
	})

	It("should succeed when .spec.gcp-id-token is set", func() {
		provider := auth.NewGCPIdTokenAuth()
		authCR := newInfisicalAuth(secretsv1beta1.GCPIdTokenAuth)
		authCR.Spec.GCPIdToken = &secretsv1beta1.GCPIdTokenAuthConfig{
			IdentityID: "identity-123",
		}

		Expect(provider.Validate(ctx, authCR)).To(Succeed())
	})
})

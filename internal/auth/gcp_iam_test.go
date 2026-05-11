package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("GCP IAM Auth", func() {
	It("should fail when .spec.gcp-iam is nil", func() {
		provider := auth.NewGCPIamAuth()
		authCR := newInfisicalAuth(secretsv1beta1.GCPIamAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.gcp-iam is not set"))
	})

	It("should succeed when .spec.gcp-iam is set", func() {
		provider := auth.NewGCPIamAuth()
		authCR := newInfisicalAuth(secretsv1beta1.GCPIamAuth)
		authCR.Spec.GCPIam = &secretsv1beta1.GCPIamAuthConfig{
			IdentityID:                "identity-123",
			ServiceAccountKeyFilePath: "/var/run/secrets/gcp/key.json",
		}

		Expect(provider.Validate(ctx, authCR)).To(Succeed())
	})
})

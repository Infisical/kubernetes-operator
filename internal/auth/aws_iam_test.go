package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("AWS IAM Auth", func() {
	It("should fail when .spec.awsIam is nil", func() {
		provider := auth.NewAWSIamAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.AWSIamAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.awsIam is not set"))
	})

	It("should succeed when .spec.awsIam is set", func() {
		provider := auth.NewAWSIamAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.AWSIamAuth)
		authCR.Spec.AWSIam = &secretsv1beta1.AWSIamAuthConfig{
			IdentityIDRef: secretsv1beta1.SecretReference{Name: "aws-identity-id", Namespace: "default", Key: "value"},
		}

		Expect(provider.Validate(ctx, authCR)).To(Succeed())
	})
})

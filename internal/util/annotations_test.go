package util_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/Infisical/infisical/k8-operator/internal/util"
)

const managedSecretPrefix = "secrets.infisical.com/managed-secret"

// namePart returns the portion of an annotation key that is bounded by the 63 byte limit.
func namePart(key string) string {
	if idx := strings.Index(key, "/"); idx != -1 {
		return key[idx+1:]
	}
	return key
}

var _ = Describe("BuildManagedSecretAnnotationKey", func() {

	It("leaves short names unchanged so existing workloads keep their annotation", func() {
		key := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, "my-app-secrets")
		Expect(key).To(Equal("secrets.infisical.com/managed-secret.my-app-secrets"))
	})

	It("leaves a name that exactly fills the limit unchanged", func() {
		// len("managed-secret.") is 15, so a 48 byte secret name yields exactly 63 bytes.
		secretName := strings.Repeat("a", 48)
		key := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, secretName)
		Expect(namePart(key)).To(HaveLen(util.AnnotationNameMaxLength))
		Expect(key).To(HaveSuffix(secretName))
	})

	DescribeTable("keeps the name part within the Kubernetes limit",
		func(secretName string) {
			key := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, secretName)
			Expect(len(namePart(key))).To(BeNumerically("<=", util.AnnotationNameMaxLength))
		},
		Entry("one byte over the limit", strings.Repeat("a", 49)),
		Entry("long service name with an environment suffix",
			"some-service-with-a-fairly-long-name-staging-secrets"),
		Entry("long service name with a multi-part secret suffix",
			"another-service-with-a-long-name-staging-common-aws-secrets"),
		Entry("maximum length resource name", strings.Repeat("a", 253)),
	)

	It("produces stable keys across repeated calls", func() {
		secretName := "some-service-with-a-fairly-long-name-staging-secrets"
		first := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, secretName)
		second := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, secretName)
		Expect(first).To(Equal(second))
	})

	It("distinguishes names that share a truncated prefix", func() {
		base := strings.Repeat("a", 60)
		first := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, base+"-one-secrets")
		second := util.BuildManagedSecretAnnotationKey(managedSecretPrefix, base+"-two-secrets")
		Expect(first).ToNot(Equal(second))
		Expect(len(namePart(first))).To(BeNumerically("<=", util.AnnotationNameMaxLength))
		Expect(len(namePart(second))).To(BeNumerically("<=", util.AnnotationNameMaxLength))
	})

	It("stays valid when the prefix alone leaves no room for a name", func() {
		longPrefix := "secrets.infisical.com/" + strings.Repeat("p", 62)
		key := util.BuildManagedSecretAnnotationKey(longPrefix, "some-secret-name")
		Expect(len(namePart(key))).To(BeNumerically("<=", util.AnnotationNameMaxLength))
	})
})

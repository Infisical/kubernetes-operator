package auth_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
)

var _ = Describe("LDAP Auth", func() {
	const (
		usernameSecretName   = "ldap-username"
		passwordSecretName   = "ldap-password"
		identityIDSecretName = "ldap-identity-id"
		namespace            = "default"
		secretKey            = "value"
	)

	It("should fail when .spec.ldap is nil", func() {
		provider := auth.NewLDAPAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.LDAPAuth)

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(".spec.ldap is not set"))
	})

	It("should fail when the referenced secrets do not exist", func() {
		provider := auth.NewLDAPAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.LDAPAuth)
		authCR.Spec.LDAP = &secretsv1beta1.LDAPAuthConfig{
			UsernameRef:   secretsv1beta1.SecretReference{Name: usernameSecretName, Namespace: namespace, Key: secretKey},
			PasswordRef:   secretsv1beta1.SecretReference{Name: passwordSecretName, Namespace: namespace, Key: secretKey},
			IdentityIDRef: secretsv1beta1.SecretReference{Name: identityIDSecretName, Namespace: namespace, Key: secretKey},
		}

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unable to fetch secret"))
	})

	It("should succeed when secrets exist, then fail after deletion", func() {
		By("creating the required secrets")
		createSecret(usernameSecretName, namespace, map[string][]byte{secretKey: []byte("admin")})
		createSecret(passwordSecretName, namespace, map[string][]byte{secretKey: []byte("password123")})
		createSecret(identityIDSecretName, namespace, map[string][]byte{secretKey: []byte("identity-abc")})

		provider := auth.NewLDAPAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.LDAPAuth)
		authCR.Spec.LDAP = &secretsv1beta1.LDAPAuthConfig{
			UsernameRef:   secretsv1beta1.SecretReference{Name: usernameSecretName, Namespace: namespace, Key: secretKey},
			PasswordRef:   secretsv1beta1.SecretReference{Name: passwordSecretName, Namespace: namespace, Key: secretKey},
			IdentityIDRef: secretsv1beta1.SecretReference{Name: identityIDSecretName, Namespace: namespace, Key: secretKey},
		}

		By("validating — should succeed")
		Expect(provider.Validate(ctx, authCR)).To(Succeed())

		By("deleting the secrets")
		deleteSecret(usernameSecretName, namespace)
		deleteSecret(passwordSecretName, namespace)
		deleteSecret(identityIDSecretName, namespace)

		By("validating again — should fail")
		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unable to fetch secret"))
	})

	It("should fail when the secret exists but the key is missing", func() {
		secretName := "ldap-wrong-key"
		createSecret(secretName, namespace, map[string][]byte{"wrong-key": []byte("data")})
		DeferCleanup(func() { deleteSecret(secretName, namespace) })

		provider := auth.NewLDAPAuth(k8sClient)
		authCR := newInfisicalAuth(secretsv1beta1.LDAPAuth)
		authCR.Spec.LDAP = &secretsv1beta1.LDAPAuthConfig{
			UsernameRef:   secretsv1beta1.SecretReference{Name: secretName, Namespace: namespace, Key: secretKey},
			PasswordRef:   secretsv1beta1.SecretReference{Name: secretName, Namespace: namespace, Key: secretKey},
			IdentityIDRef: secretsv1beta1.SecretReference{Name: secretName, Namespace: namespace, Key: secretKey},
		}

		err := provider.Validate(ctx, authCR)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("has no value for key"))
	})
})

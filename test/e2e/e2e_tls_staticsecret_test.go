package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
)

var _ = Describe("InfisicalStaticSecret with TLS", Ordered, ContinueOnFailure, func() {
	var (
		ctx     context.Context
		api     *infra.NodeJSService
		k       client.Client
		project *infra.ProjectSeed
		authRef secretsv1beta1.NamespacedName
	)

	BeforeAll(func() {
		ctx = context.Background()
		api = testInfra.NodeJS()
		k = testManager.Client()

		Expect(testManager.InClusterTLSAPIURL()).NotTo(BeEmpty(), "TLS proxy not configured")

		project = api.CreateProject(GinkgoT(), "tls-static-secret")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		identity := api.CreateIdentity(GinkgoT(), "tls-test-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds := api.SetupUniversalAuth(GinkgoT(), identity.ID)

		credentialSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-ua-credentials",
				Namespace: testNamespace,
			},
			StringData: map[string]string{
				"clientId":     creds.ClientID,
				"clientSecret": creds.ClientSecret,
			},
		}
		Expect(k.Create(ctx, credentialSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, credentialSecret)) })

		caSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-ca-cert",
				Namespace: testNamespace,
			},
			Data: map[string][]byte{
				"ca.crt": testInfra.TLS().CAPem,
			},
		}
		Expect(k.Create(ctx, caSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, caSecret)) })

		connection := &secretsv1beta1.InfisicalConnection{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-connection",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalConnectionSpec{
				Address: testManager.InClusterTLSAPIURL(),
				TLS: &secretsv1beta1.TLSConfig{
					CaCertificate: &secretsv1beta1.SecretReference{
						Name:      caSecret.Name,
						Namespace: caSecret.Namespace,
						Key:       "ca.crt",
					},
				},
			},
		}
		Expect(k.Create(ctx, connection)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, connection)) })

		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-auth",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: secretsv1beta1.NamespacedName{
					Name:      connection.Name,
					Namespace: connection.Namespace,
				},
				Method: secretsv1beta1.UniversalAuth,
				Universal: &secretsv1beta1.UniversalAuthConfig{
					ClientIdRef: secretsv1beta1.SecretReference{
						Name:      credentialSecret.Name,
						Namespace: credentialSecret.Namespace,
						Key:       "clientId",
					},
					ClientSecretRef: secretsv1beta1.SecretReference{
						Name:      credentialSecret.Name,
						Namespace: credentialSecret.Namespace,
						Key:       "clientSecret",
					},
				},
			},
		}
		Expect(k.Create(ctx, auth)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, auth)) })

		authRef = secretsv1beta1.NamespacedName{
			Name:      auth.Name,
			Namespace: auth.Namespace,
		}

		By("waiting for TLS InfisicalConnection to become ready")
		Eventually(func(g Gomega) {
			var conn secretsv1beta1.InfisicalConnection
			g.Expect(k.Get(ctx, types.NamespacedName{Name: connection.Name, Namespace: connection.Namespace}, &conn)).To(Succeed())
			cond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalConnection has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalConnection not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("waiting for TLS InfisicalAuth to become ready")
		Eventually(func(g Gomega) {
			var a secretsv1beta1.InfisicalAuth
			g.Expect(k.Get(ctx, types.NamespacedName{Name: auth.Name, Namespace: auth.Namespace}, &a)).To(Succeed())
			cond := meta.FindStatusCondition(a.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalAuth has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalAuth not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	checkStaticSecretStatus := func(g Gomega, crdName string) {
		GinkgoHelper()
		var ss secretsv1beta1.InfisicalStaticSecret
		if err := k.Get(ctx, types.NamespacedName{Name: crdName, Namespace: testNamespace}, &ss); err != nil {
			return
		}
		cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
		if cond != nil && cond.Status == metav1.ConditionFalse {
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalStaticSecret %q reconciliation failed: %s", crdName, cond.Message)
		}
	}

	It("should sync secrets over TLS", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "tls-basic")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/tls-basic", "TLS_KEY", "tls-value", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/tls-basic", "TLS_HOST", "secure.internal", nil)

		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-basic-sync",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalStaticSecretSpec{
				InfisicalAuthRef: authRef,
				SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
				Sources: []secretsv1beta1.SecretSource{{
					ProjectId:       project.ID,
					EnvironmentSlug: project.EnvSlug,
					SecretPath:      "/tls-basic",
				}},
				Targets: []secretsv1beta1.SecretTarget{{
					Name:           "e2e-tls-basic-synced",
					Namespace:      testNamespace,
					Kind:           secretsv1beta1.SecretTargetKindSecret,
					SecretType:     corev1.SecretTypeOpaque,
					CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				}},
			},
		}
		Expect(k.Create(ctx, ss)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, ss)) })

		var synced corev1.Secret
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, "e2e-tls-basic-sync")
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-tls-basic-synced", Namespace: testNamespace}, &synced)).To(Succeed())
			g.Expect(synced.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Expect(synced.Data).To(HaveKeyWithValue("TLS_KEY", []byte("tls-value")))
		Expect(synced.Data).To(HaveKeyWithValue("TLS_HOST", []byte("secure.internal")))
	})

	It("should sync using project slug over TLS", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "tls-slug")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/tls-slug", "SLUG_TLS_KEY", "slug-tls-value", nil)

		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-slug-sync",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalStaticSecretSpec{
				InfisicalAuthRef: authRef,
				SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
				Sources: []secretsv1beta1.SecretSource{{
					ProjectSlug:     project.Slug,
					EnvironmentSlug: project.EnvSlug,
					SecretPath:      "/tls-slug",
				}},
				Targets: []secretsv1beta1.SecretTarget{{
					Name:           "e2e-tls-slug-synced",
					Namespace:      testNamespace,
					Kind:           secretsv1beta1.SecretTargetKindSecret,
					SecretType:     corev1.SecretTypeOpaque,
					CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				}},
			},
		}
		Expect(k.Create(ctx, ss)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, ss)) })

		var synced corev1.Secret
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, "e2e-tls-slug-sync")
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-tls-slug-synced", Namespace: testNamespace}, &synced)).To(Succeed())
			g.Expect(synced.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Expect(synced.Data).To(HaveKeyWithValue("SLUG_TLS_KEY", []byte("slug-tls-value")))
	})

	It("should sync recursively over TLS", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "tls-recursive")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/tls-recursive", "ROOT_VAL", "root", nil)
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/tls-recursive", "nested")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/tls-recursive/nested", "NESTED_VAL", "nested", nil)

		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-tls-recursive-sync",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalStaticSecretSpec{
				InfisicalAuthRef: authRef,
				SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
				Sources: []secretsv1beta1.SecretSource{{
					ProjectId:       project.ID,
					EnvironmentSlug: project.EnvSlug,
					SecretPath:      "/tls-recursive",
					Recursive:       true,
				}},
				Targets: []secretsv1beta1.SecretTarget{{
					Name:           "e2e-tls-recursive-synced",
					Namespace:      testNamespace,
					Kind:           secretsv1beta1.SecretTargetKindSecret,
					SecretType:     corev1.SecretTypeOpaque,
					CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				}},
			},
		}
		Expect(k.Create(ctx, ss)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, ss)) })

		var synced corev1.Secret
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, "e2e-tls-recursive-sync")
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-tls-recursive-synced", Namespace: testNamespace}, &synced)).To(Succeed())
			g.Expect(synced.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Expect(synced.Data).To(HaveKeyWithValue("ROOT_VAL", []byte("root")))
		Expect(synced.Data).To(HaveKeyWithValue("NESTED_VAL", []byte("nested")))
	})
})

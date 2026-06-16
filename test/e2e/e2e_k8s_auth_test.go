package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
)

var _ = Describe("InfisicalStaticSecret with Kubernetes Auth", Ordered, ContinueOnFailure, func() {
	var (
		ctx     context.Context
		api     *infra.NodeJSService
		k       client.Client
		project *infra.ProjectSeed
		authRef secretsv1beta1.NamespacedName

		scopedBasePath string
		saName         = "e2e-k8s-auth-sa"
	)

	BeforeAll(func() {
		ctx = context.Background()
		api = testInfra.NodeJS()
		k = testManager.Client()
		clusterInfo := testManager.ClusterInfo()

		project = api.CreateProject(GinkgoT(), "k8s-auth")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		// -- K8s resources: service account for auth + token reviewer ---------

		sa := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: testNamespace},
		}
		Expect(k.Create(ctx, sa)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, sa)) })

		reviewerSA := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-token-reviewer", Namespace: testNamespace},
		}
		Expect(k.Create(ctx, reviewerSA)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, reviewerSA)) })

		reviewerBinding := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-token-reviewer-binding"},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "system:auth-delegator",
			},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      reviewerSA.Name,
				Namespace: reviewerSA.Namespace,
			}},
		}
		Expect(k.Create(ctx, reviewerBinding)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, reviewerBinding)) })

		clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		Expect(err).NotTo(HaveOccurred())

		reviewerToken, err := clientset.CoreV1().ServiceAccounts(reviewerSA.Namespace).
			CreateToken(ctx, reviewerSA.Name, &authenticationv1.TokenRequest{
				Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: ptr(int64(3600))},
			}, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred())

		// -- Infisical identity with K8s Auth + scoped role -------------------

		identity := api.CreateIdentity(GinkgoT(), "k8s-auth-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.SetupKubernetesAuth(GinkgoT(), identity.ID, infra.KubernetesAuthSetup{
			KubernetesHost:    clusterInfo.Host,
			CACert:            clusterInfo.CACert,
			TokenReviewerJwt:  reviewerToken.Status.Token,
			AllowedNamespaces: testNamespace,
			AllowedNames:      saName,
		})

		scopedBasePath = fmt.Sprintf("/kubernetes/%s/%s", testNamespace, saName)

		role := api.CreateProjectRole(GinkgoT(), project.ID, "scoped-reader", "Scoped Reader", []infra.Permission{
			{
				Subject: "secrets",
				Action:  []string{"read"},
				Conditions: map[string]any{
					"secretPath": map[string]any{"$glob": scopedBasePath + "/**"},
				},
			},
		})

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, []infra.RoleAssignment{{Role: role.Slug}})

		// -- CRDs: store identity ID, create connection + auth ----------------

		identityIDSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-k8s-identity-id", Namespace: testNamespace},
			StringData: map[string]string{"identityId": identity.ID},
		}
		Expect(k.Create(ctx, identityIDSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, identityIDSecret)) })

		connection := &secretsv1beta1.InfisicalConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-k8s-connection", Namespace: testNamespace},
			Spec:       secretsv1beta1.InfisicalConnectionSpec{Address: testManager.InClusterAPIURL()},
		}
		Expect(k.Create(ctx, connection)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, connection)) })

		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-k8s-auth", Namespace: testNamespace},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: secretsv1beta1.NamespacedName{
					Name:      connection.Name,
					Namespace: connection.Namespace,
				},
				Method: secretsv1beta1.KubernetesAuth,
				Kubernetes: &secretsv1beta1.KubernetesAuthConfig{
					IdentityIDRef: secretsv1beta1.SecretReference{
						Name:      identityIDSecret.Name,
						Namespace: identityIDSecret.Namespace,
						Key:       "identityId",
					},
					ServiceAccountRef: secretsv1beta1.NamespacedName{
						Name:      saName,
						Namespace: testNamespace,
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

		By("waiting for InfisicalConnection to become ready")
		Eventually(func(g Gomega) {
			var conn secretsv1beta1.InfisicalConnection
			g.Expect(k.Get(ctx, types.NamespacedName{Name: connection.Name, Namespace: connection.Namespace}, &conn)).To(Succeed())
			cond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalConnection has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalConnection not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("waiting for InfisicalAuth to become ready")
		Eventually(func(g Gomega) {
			var a secretsv1beta1.InfisicalAuth
			g.Expect(k.Get(ctx, types.NamespacedName{Name: auth.Name, Namespace: auth.Namespace}, &a)).To(Succeed())
			cond := meta.FindStatusCondition(a.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalAuth has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalAuth not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	createStaticSecret := func(name string, spec secretsv1beta1.InfisicalStaticSecretSpec) *secretsv1beta1.InfisicalStaticSecret {
		GinkgoHelper()
		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNamespace,
			},
			Spec: spec,
		}
		Expect(k.Create(ctx, ss)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, ss)) })
		return ss
	}

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

	expectSecret := func(targetName, crdName string) *corev1.Secret {
		GinkgoHelper()
		var secret corev1.Secret
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, crdName)
			g.Expect(k.Get(ctx, types.NamespacedName{Name: targetName, Namespace: testNamespace}, &secret)).To(Succeed())
			g.Expect(secret.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
		return &secret
	}

	expectReconcileFailure := func(crdName, messageSubstring string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			var ss secretsv1beta1.InfisicalStaticSecret
			g.Expect(k.Get(ctx, types.NamespacedName{Name: crdName, Namespace: testNamespace}, &ss)).To(Succeed())
			cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil(), "no LastReconcileStatus condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(ContainSubstring(messageSubstring))
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	}

	// ----- Test cases --------------------------------------------------------

	It("should sync secrets from a path within the role scope", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "kubernetes")
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/kubernetes", testNamespace)
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, fmt.Sprintf("/kubernetes/%s", testNamespace), saName)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, scopedBasePath, "DB_URL", "postgres://db:5432", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, scopedBasePath, "API_KEY", "secret-123", nil)

		createStaticSecret("e2e-k8s-scoped-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      scopedBasePath,
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-k8s-scoped-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-k8s-scoped-synced", "e2e-k8s-scoped-sync")
		Expect(synced.Data).To(HaveKeyWithValue("DB_URL", []byte("postgres://db:5432")))
		Expect(synced.Data).To(HaveKeyWithValue("API_KEY", []byte("secret-123")))
	})

	It("should not leak secrets from a path outside the role scope", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "no-access")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/no-access", "FORBIDDEN_KEY", "forbidden-val", nil)

		createStaticSecret("e2e-k8s-outside-role", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/no-access",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-k8s-outside-role-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		// Infisical RBAC filters results rather than rejecting the request,
		// so reconciliation succeeds but no secrets are synced.
		Eventually(func(g Gomega) {
			var ss secretsv1beta1.InfisicalStaticSecret
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-k8s-outside-role", Namespace: testNamespace}, &ss)).To(Succeed())
			cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil(), "no LastReconcileStatus condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())

		var secret corev1.Secret
		Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-k8s-outside-role-synced", Namespace: testNamespace}, &secret)).To(Succeed())
		Expect(secret.Data).NotTo(HaveKey("FORBIDDEN_KEY"),
			"secret from a path outside the role scope must not be synced")
	})

	It("should fail when the source path does not exist", func() {
		createStaticSecret("e2e-k8s-nonexistent", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      scopedBasePath + "/this-does-not-exist",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-k8s-nonexistent-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		expectReconcileFailure("e2e-k8s-nonexistent", "failed to list secrets")
	})

	It("should fail when the target namespace is outside the operator scope", func() {
		outsideNs := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-k8s-outside-ns"},
		}
		Expect(k.Create(ctx, outsideNs)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, outsideNs)) })

		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, scopedBasePath, "OUTSIDE_NS_KEY", "val", nil)

		createStaticSecret("e2e-k8s-outside-ns", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      scopedBasePath,
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-k8s-outside-ns-synced",
				Namespace:      "e2e-k8s-outside-ns",
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		expectReconcileFailure("e2e-k8s-outside-ns", "namespace-scoped")
	})
})

func ptr[T any](v T) *T { return &v }

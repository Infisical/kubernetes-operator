package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1alpha1 "github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

const testNamespace = "default"

var _ = Describe("InfisicalStaticSecret", Ordered, ContinueOnFailure, func() {
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

		project = api.CreateProject(GinkgoT(), "static-secret")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		identity := api.CreateIdentity(GinkgoT(), "test-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds := api.SetupUniversalAuth(GinkgoT(), identity.ID)

		credentialSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-ua-credentials",
				Namespace: testNamespace,
			},
			StringData: map[string]string{
				"clientId":     creds.ClientID,
				"clientSecret": creds.ClientSecret,
			},
		}
		Expect(k.Create(ctx, credentialSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, credentialSecret)) })

		connection := &secretsv1beta1.InfisicalConnection{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-connection",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalConnectionSpec{
				Address: testManager.InClusterAPIURL(),
			},
		}
		Expect(k.Create(ctx, connection)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, connection)) })

		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-auth",
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

	// checkStaticSecretStatus fetches the InfisicalStaticSecret and fails
	// immediately if the controller has recorded a reconciliation error,
	// so tests surface the real problem instead of timing out.
	checkStaticSecretStatus := func(g Gomega, crdName string) {
		GinkgoHelper()
		var ss secretsv1beta1.InfisicalStaticSecret
		if err := k.Get(ctx, types.NamespacedName{Name: crdName, Namespace: testNamespace}, &ss); err != nil {
			return // CRD not yet visible; let the outer Eventually keep polling
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

	expectConfigMap := func(targetName, crdName string) *corev1.ConfigMap {
		GinkgoHelper()
		var cm corev1.ConfigMap
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, crdName)
			g.Expect(k.Get(ctx, types.NamespacedName{Name: targetName, Namespace: testNamespace}, &cm)).To(Succeed())
			g.Expect(cm.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
		return &cm
	}

	expectSecretData := func(secret *corev1.Secret, expected map[string]string) {
		GinkgoHelper()
		for key, want := range expected {
			Expect(secret.Data).To(HaveKeyWithValue(key, []byte(want)),
				"secret data key %q", key)
		}
	}

	It("should sync basic secrets", func() {
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "DB_HOST", "localhost", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "DB_PORT", "5432", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "API_KEY", "super-secret-key", nil)

		createStaticSecret("e2e-basic-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-basic-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-basic-synced", "e2e-basic-sync")
		expectSecretData(synced, map[string]string{
			"DB_HOST": "localhost",
			"DB_PORT": "5432",
			"API_KEY": "super-secret-key",
		})
	})

	It("should sync from a nested folder", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "database")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/database", "HOST", "db.internal", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/database", "PORT", "3306", nil)

		createStaticSecret("e2e-folder-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/database",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-folder-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-folder-synced", "e2e-folder-sync")
		expectSecretData(synced, map[string]string{
			"HOST": "db.internal",
			"PORT": "3306",
		})
	})

	It("should sync recursively", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "services")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/services", "ROOT_KEY", "root-val", nil)
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/services", "auth")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/services/auth", "JWT_SECRET", "jwt-val", nil)

		createStaticSecret("e2e-recursive-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/services",
				Recursive:       true,
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-recursive-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-recursive-synced", "e2e-recursive-sync")
		expectSecretData(synced, map[string]string{
			"ROOT_KEY":   "root-val",
			"JWT_SECRET": "jwt-val",
		})
	})

	It("should sync from multiple sources", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "frontend")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/frontend", "NEXT_PUBLIC_URL", "https://app.test", nil)

		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "backend")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/backend", "INTERNAL_URL", "http://api.internal", nil)

		createStaticSecret("e2e-multisource-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{
				{
					ProjectId:       project.ID,
					EnvironmentSlug: project.EnvSlug,
					SecretPath:      "/frontend",
				},
				{
					ProjectId:       project.ID,
					EnvironmentSlug: project.EnvSlug,
					SecretPath:      "/backend",
				},
			},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-multisource-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-multisource-synced", "e2e-multisource-sync")
		expectSecretData(synced, map[string]string{
			"NEXT_PUBLIC_URL": "https://app.test",
			"INTERNAL_URL":    "http://api.internal",
		})
	})

	It("should sync to a ConfigMap target", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "config")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/config", "LOG_LEVEL", "debug", nil)

		createStaticSecret("e2e-configmap-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/config",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-configmap-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindConfigMap,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		cm := expectConfigMap("e2e-configmap-synced", "e2e-configmap-sync")
		Expect(cm.Data).To(HaveKeyWithValue("LOG_LEVEL", "debug"))
	})

	It("should sync with templates", func() {
		tplUser := infra.RandomID("user")
		tplPass := infra.RandomID("pass")
		tplHost := infra.RandomID("host-") + ".test"
		tplDB := infra.RandomID("db")

		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "templated")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/templated", "PG_USER", tplUser, nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/templated", "PG_PASS", tplPass, nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/templated", "PG_HOST", tplHost, nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/templated", "PG_DB", tplDB, nil)

		createStaticSecret("e2e-template-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/templated",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-template-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				Template: &secretsv1beta1.SecretTemplate{
					EngineVersion: "v1",
					Data: secretsv1beta1.SecretTemplateData{
						Map: map[string]string{
							"DSN": "postgres://{{ .PG_USER.Value }}:{{ .PG_PASS.Value }}@{{ .PG_HOST.Value }}/{{ .PG_DB.Value }}",
						},
					},
				},
			}},
		})

		synced := expectSecret("e2e-template-synced", "e2e-template-sync")
		expectSecretData(synced, map[string]string{
			"DSN": fmt.Sprintf("postgres://%s:%s@%s/%s", tplUser, tplPass, tplHost, tplDB),
		})
	})

	It("should resolve duplicate keys from different paths via secretFrom", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "resolve-tpl")
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/resolve-tpl", "db")
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/resolve-tpl", "cache")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/resolve-tpl/db", "HOST", "db.internal", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/resolve-tpl/cache", "HOST", "cache.internal", nil)

		createStaticSecret("e2e-resolve-path-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/resolve-tpl",
				Recursive:       true,
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-resolve-path-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				Template: &secretsv1beta1.SecretTemplate{
					EngineVersion: "v1",
					Data: secretsv1beta1.SecretTemplateData{
						Map: map[string]string{
							"DB_HOST":    `{{ secretFrom "/resolve-tpl/db" "HOST" }}`,
							"CACHE_HOST": `{{ (secretFrom "/resolve-tpl/cache" "HOST").Value }}`,
						},
					},
				},
			}},
		})

		synced := expectSecret("e2e-resolve-path-synced", "e2e-resolve-path-sync")
		expectSecretData(synced, map[string]string{
			"DB_HOST":    "db.internal",
			"CACHE_HOST": "cache.internal",
		})
	})

	It("should apply target metadata", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "labeled")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/labeled", "TOKEN", "abc123", nil)

		createStaticSecret("e2e-metadata-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/labeled",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-metadata-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				Metadata: &secretsv1beta1.SecretTargetMetadata{
					Annotations: map[string]string{"infisical.com/env": "test"},
					Labels:      map[string]string{"app": "e2e"},
				},
			}},
		})

		synced := expectSecret("e2e-metadata-synced", "e2e-metadata-sync")
		Expect(synced.Annotations).To(HaveKeyWithValue("infisical.com/env", "test"))
		Expect(synced.Labels).To(HaveKeyWithValue("app", "e2e"))
		expectSecretData(synced, map[string]string{"TOKEN": "abc123"})
	})

	It("should merge metadata with pre-existing secret and propagate updates", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "merged")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/merged", "SECRET_A", "val-a", nil)

		preExisting := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "e2e-merged-metadata",
				Namespace:   testNamespace,
				Annotations: map[string]string{"custom.io/note": "keep-me"},
				Labels:      map[string]string{"existing": "true"},
			},
			StringData: map[string]string{"placeholder": "old"},
		}
		Expect(k.Create(ctx, preExisting)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, preExisting)) })

		ss := createStaticSecret("e2e-merged-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/merged",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-merged-metadata",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOrphan,
			}},
		})
		ss.Annotations = map[string]string{"infisical.com/env": "staging"}
		ss.Labels = map[string]string{"app": "e2e"}
		Expect(k.Update(ctx, ss)).To(Succeed())

		var synced corev1.Secret
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, "e2e-merged-sync")
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-merged-metadata", Namespace: testNamespace}, &synced)).To(Succeed())
			g.Expect(synced.Labels).To(HaveKeyWithValue("app", "e2e"))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Expect(synced.Labels).To(HaveKeyWithValue("existing", "true"))
		Expect(synced.Labels).To(HaveKeyWithValue("app", "e2e"))
		Expect(synced.Annotations).To(HaveKeyWithValue("custom.io/note", "keep-me"))
		Expect(synced.Annotations).To(HaveKeyWithValue("infisical.com/env", "staging"))
		expectSecretData(&synced, map[string]string{"SECRET_A": "val-a"})

		Expect(k.Get(ctx, types.NamespacedName{Name: ss.Name, Namespace: ss.Namespace}, ss)).To(Succeed())
		ss.Annotations["infisical.com/env"] = "production"
		ss.Annotations["infisical.com/owner"] = "platform-team"
		ss.Labels["tier"] = "backend"
		Expect(k.Update(ctx, ss)).To(Succeed())

		var updated corev1.Secret
		Eventually(func(g Gomega) {
			checkStaticSecretStatus(g, "e2e-merged-sync")
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-merged-metadata", Namespace: testNamespace}, &updated)).To(Succeed())
			g.Expect(updated.Annotations).To(HaveKeyWithValue("infisical.com/env", "production"))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Expect(updated.Labels).To(HaveKeyWithValue("existing", "true"))
		Expect(updated.Labels).To(HaveKeyWithValue("app", "e2e"))
		Expect(updated.Labels).To(HaveKeyWithValue("tier", "backend"))
		Expect(updated.Annotations).To(HaveKeyWithValue("custom.io/note", "keep-me"))
		Expect(updated.Annotations).To(HaveKeyWithValue("infisical.com/env", "production"))
		Expect(updated.Annotations).To(HaveKeyWithValue("infisical.com/owner", "platform-team"))
		expectSecretData(&updated, map[string]string{"SECRET_A": "val-a"})
	})

	It("should sync to multiple targets", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "multitarget")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/multitarget", "SHARED_KEY", "shared-val", nil)

		createStaticSecret("e2e-multitarget-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/multitarget",
			}},
			Targets: []secretsv1beta1.SecretTarget{
				{
					Name:           "e2e-mt-secret",
					Namespace:      testNamespace,
					Kind:           secretsv1beta1.SecretTargetKindSecret,
					SecretType:     corev1.SecretTypeOpaque,
					CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				},
				{
					Name:           "e2e-mt-configmap",
					Namespace:      testNamespace,
					Kind:           secretsv1beta1.SecretTargetKindConfigMap,
					CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				},
			},
		})

		synced := expectSecret("e2e-mt-secret", "e2e-multitarget-sync")
		expectSecretData(synced, map[string]string{"SHARED_KEY": "shared-val"})

		cm := expectConfigMap("e2e-mt-configmap", "e2e-multitarget-sync")
		Expect(cm.Data).To(HaveKeyWithValue("SHARED_KEY", "shared-val"))
	})

	It("should not break if secret name is too long", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "superlong")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/superlong", "SHARED_KEY", "shared-val", nil)

		const secretName = "e2e-mt-secret-with-a-super-long-name-that-would-cause-k8s-to-reject-annotation"

		replicas := int32(1)
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-superlong-consumer",
				Namespace: testNamespace,
				Annotations: map[string]string{
					"secrets.infisical.com/auto-reload": "true",
				},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "e2e-superlong-consumer"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "e2e-superlong-consumer"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "main",
							Image: "busybox",
							EnvFrom: []corev1.EnvFromSource{{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								},
							}},
						}},
					},
				},
			},
		}
		Expect(k.Create(ctx, dep)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, dep)) })

		createStaticSecret("e2e-superlong-secret-name-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/superlong",
			}},
			Targets: []secretsv1beta1.SecretTarget{
				{
					Name:           secretName,
					Namespace:      testNamespace,
					Kind:           secretsv1beta1.SecretTargetKindSecret,
					SecretType:     corev1.SecretTypeOpaque,
					CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				},
			},
		})

		synced := expectSecret(secretName, "e2e-superlong-secret-name-sync")
		expectSecretData(synced, map[string]string{"SHARED_KEY": "shared-val"})

		annotationKey := util.ComputeManagedSecretAnnotation(secretName)
		Eventually(func(g Gomega) {
			var updated appsv1.Deployment
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-superlong-consumer", Namespace: testNamespace}, &updated)).To(Succeed())
			g.Expect(updated.Annotations).To(HaveKey(annotationKey), "auto-reload annotation was not propagated to deployment")
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	It("should sync using project slug instead of project ID", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "slug-test")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/slug-test", "SLUG_KEY", "slug-value", nil)

		createStaticSecret("e2e-slug-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectSlug:     project.Slug,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/slug-test",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-slug-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-slug-synced", "e2e-slug-sync")
		expectSecretData(synced, map[string]string{
			"SLUG_KEY": "slug-value",
		})
	})

	It("should resolve imported secrets via secretFrom in templates", func() {
		// Shallow import source
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "import-tpl-source")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-source", "IMPORTED_KEY", "imported-value", nil)

		// Deep import source
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "import-tpl-deep")
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-deep", "nested")
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-deep/nested", "deep")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-deep/nested/deep", "DEEP_KEY", "deep-value", nil)

		// Target folder with a local secret where imports will be created and two imports
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "import-tpl-target")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-target", "LOCAL_KEY", "local-value", nil)
		api.CreateSecretImport(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-target", project.EnvSlug, "/import-tpl-source")
		api.CreateSecretImport(GinkgoT(), project.ID, project.EnvSlug, "/import-tpl-target", project.EnvSlug, "/import-tpl-deep/nested/deep")

		createStaticSecret("e2e-import-tpl-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/import-tpl-target",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-import-tpl-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
				Template: &secretsv1beta1.SecretTemplate{
					EngineVersion: "v1",
					Data: secretsv1beta1.SecretTemplateData{
						Map: map[string]string{
							"FROM_IMPORT": `{{ secretFrom "/import-tpl-target" "IMPORTED_KEY" }}`,
							"FROM_DEEP":   `{{ secretFrom "/import-tpl-target" "DEEP_KEY" }}`,
							"LOCAL":       `{{ secretFrom "/import-tpl-target" "LOCAL_KEY" }}`,
						},
					},
				},
			}},
		})

		synced := expectSecret("e2e-import-tpl-synced", "e2e-import-tpl-sync")
		expectSecretData(synced, map[string]string{
			"FROM_IMPORT": "imported-value",
			"FROM_DEEP":   "deep-value",
			"LOCAL":       "local-value",
		})
	})

	It("should sync with secret imports", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "shared-lib")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/shared-lib", "REDIS_URL", "redis://cache:6379", nil)

		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "app-with-import")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/app-with-import", "APP_NAME", "my-app", nil)
		api.CreateSecretImport(GinkgoT(), project.ID, project.EnvSlug, "/app-with-import", project.EnvSlug, "/shared-lib")

		createStaticSecret("e2e-import-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/app-with-import",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-import-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-import-synced", "e2e-import-sync")
		expectSecretData(synced, map[string]string{
			"APP_NAME":  "my-app",
			"REDIS_URL": "redis://cache:6379",
		})
	})

	It("should adopt a target owned by an InfisicalSecret and keep it after the InfisicalSecret is deleted", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "adopt")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/adopt", "ADOPT_KEY", "adopt-value", nil)

		const targetName = "e2e-adopt-synced"
		targetKey := types.NamespacedName{Name: targetName, Namespace: testNamespace}

		By("creating an InfisicalSecret that owns the target")
		infisicalSecret := &secretsv1alpha1.InfisicalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-adopt-legacy",
				Namespace: testNamespace,
			},
			Spec: secretsv1alpha1.InfisicalSecretSpec{
				HostAPI: testManager.InClusterAPIURL(),
				Authentication: secretsv1alpha1.Authentication{
					UniversalAuth: secretsv1alpha1.UniversalAuthDetails{
						CredentialsRef: secretsv1alpha1.KubeSecretReference{
							SecretName:      "e2e-ua-credentials",
							SecretNamespace: testNamespace,
						},
						SecretsScope: secretsv1alpha1.MachineIdentityScopeInWorkspace{
							ProjectID:   project.ID,
							EnvSlug:     project.EnvSlug,
							SecretsPath: "/adopt",
						},
					},
				},
				ManagedKubeSecretReferences: []secretsv1alpha1.ManagedKubeSecretConfig{{
					SecretName:      targetName,
					SecretNamespace: testNamespace,
					SecretType:      string(corev1.SecretTypeOpaque),
					CreationPolicy:  "Owner",
				}},
			},
		}
		Expect(k.Create(ctx, infisicalSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, infisicalSecret)) })

		var original corev1.Secret
		Eventually(func(g Gomega) {
			g.Expect(k.Get(ctx, targetKey, &original)).To(Succeed())
			g.Expect(metav1.IsControlledBy(&original, infisicalSecret)).To(BeTrue(),
				"target is not controlled by the InfisicalSecret: %v", original.OwnerReferences)
			g.Expect(original.Data).To(HaveKeyWithValue("ADOPT_KEY", []byte("adopt-value")))
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("creating an InfisicalStaticSecret targeting the same secret")
		staticSecret := createStaticSecret("e2e-adopt-sync", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/adopt",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           targetName,
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		By("waiting for the InfisicalStaticSecret to reconcile")
		Eventually(func(g Gomega) {
			var ss secretsv1beta1.InfisicalStaticSecret
			g.Expect(k.Get(ctx, types.NamespacedName{Name: staticSecret.Name, Namespace: testNamespace}, &ss)).To(Succeed())
			cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil(), "InfisicalStaticSecret has not reconciled yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalStaticSecret reconciliation failed: %s", cond.Message)
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("deleting the InfisicalSecret")
		Expect(k.Delete(ctx, infisicalSecret)).To(Succeed())
		Eventually(func() error {
			return k.Get(ctx, types.NamespacedName{Name: infisicalSecret.Name, Namespace: testNamespace}, &secretsv1alpha1.InfisicalSecret{})
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Satisfy(k8sErrors.IsNotFound))

		By("checking the target was adopted by the InfisicalStaticSecret, not recreated")
		Eventually(func(g Gomega) {
			var secret corev1.Secret
			g.Expect(k.Get(ctx, targetKey, &secret)).To(Succeed())
			g.Expect(secret.UID).To(Equal(original.UID), "target was recreated")
			g.Expect(metav1.IsControlledBy(&secret, staticSecret)).To(BeTrue(),
				"target is not controlled by the InfisicalStaticSecret: %v", secret.OwnerReferences)
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
		Consistently(func(g Gomega) {
			var secret corev1.Secret
			g.Expect(k.Get(ctx, targetKey, &secret)).To(Succeed())
			g.Expect(secret.UID).To(Equal(original.UID), "target was recreated")
		}).WithTimeout(10 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	It("should expose Prometheus metrics for static secrets", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "metrics")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/metrics", "METRICS_KEY", "metrics-value", nil)

		// A workload opted into auto-reload, created before the secret so that the
		// first sync redeploys it and moves the redeploy counter off zero.
		replicas := int32(1)
		consumer := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "e2e-metrics-consumer",
				Namespace:   testNamespace,
				Annotations: map[string]string{"secrets.infisical.com/auto-reload": "true"},
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "e2e-metrics-consumer"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "e2e-metrics-consumer"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{
							Name:  "main",
							Image: "busybox",
							EnvFrom: []corev1.EnvFromSource{{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: "e2e-metrics-synced"},
								},
							}},
						}},
					},
				},
			},
		}
		Expect(k.Create(ctx, consumer)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, consumer)) })

		healthy := createStaticSecret("e2e-metrics-healthy", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "2m"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: project.EnvSlug,
				SecretPath:      "/metrics",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-metrics-synced",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})
		broken := createStaticSecret("e2e-metrics-broken", secretsv1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
			Sources: []secretsv1beta1.SecretSource{{
				ProjectId:       project.ID,
				EnvironmentSlug: "does-not-exist",
				SecretPath:      "/",
			}},
			Targets: []secretsv1beta1.SecretTarget{{
				Name:           "e2e-metrics-broken-target",
				Namespace:      testNamespace,
				Kind:           secretsv1beta1.SecretTargetKindSecret,
				SecretType:     corev1.SecretTypeOpaque,
				CreationPolicy: secretsv1beta1.CreationPolicyOwner,
			}},
		})

		synced := expectSecret("e2e-metrics-synced", healthy.Name)
		expectSecretData(synced, map[string]string{"METRICS_KEY": "metrics-value"})

		Eventually(func(g Gomega) {
			var ss secretsv1beta1.InfisicalStaticSecret
			g.Expect(k.Get(ctx, types.NamespacedName{Name: broken.Name, Namespace: testNamespace}, &ss)).To(Succeed())
			cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil(), "InfisicalStaticSecret has no LastReconcileStatus condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		clientset, err := kubernetes.NewForConfig(ctrl.GetConfigOrDie())
		Expect(err).NotTo(HaveOccurred())

		// scrapeMetrics returns the operator's infisical_* metric lines. It reads /metrics through
		// the API server's service proxy, which needs no port-forward and accepts the endpoint's
		// self-signed certificate. Labels are rendered in alphabetical order.
		scrapeMetrics := func(g Gomega) []string {
			var services corev1.ServiceList
			g.Expect(k.List(ctx, &services, client.InNamespace(operatorNamespace))).To(Succeed())
			metricsService := ""
			for _, service := range services.Items {
				if strings.HasSuffix(service.Name, "-controller-manager-metrics-service") {
					metricsService = service.Name
				}
			}
			g.Expect(metricsService).NotTo(BeEmpty(), "operator metrics service not found")

			raw, err := clientset.CoreV1().RESTClient().Get().
				Namespace(operatorNamespace).
				Resource("services").
				Name("https:" + metricsService + ":https").
				SubResource("proxy").
				Suffix("metrics").
				DoRaw(ctx)
			g.Expect(err).NotTo(HaveOccurred())

			var lines []string
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(line, "infisical_") {
					lines = append(lines, line)
				}
			}
			return lines
		}

		namespaceLabels := fmt.Sprintf(`namespace=%q`, testNamespace)
		healthyLabels := fmt.Sprintf(`name=%q,namespace=%q`, healthy.Name, testNamespace)
		brokenLabels := fmt.Sprintf(`name=%q,namespace=%q`, broken.Name, testNamespace)

		By("scraping the operator's /metrics endpoint")
		Eventually(func(g Gomega) {
			// Compare the per-namespace counts with the API server's view rather than fixed
			// numbers, since resources from earlier specs may still be going away.
			var list secretsv1beta1.InfisicalStaticSecretList
			g.Expect(k.List(ctx, &list, client.InNamespace(testNamespace))).To(Succeed())
			failed := 0
			for _, ss := range list.Items {
				cond := meta.FindStatusCondition(ss.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
				if cond != nil && cond.Status == metav1.ConditionFalse {
					failed++
				}
			}

			lines := scrapeMetrics(g)
			g.Expect(lines).To(ContainElements(
				fmt.Sprintf("infisical_static_secrets{%s} %d", namespaceLabels, len(list.Items)),
				fmt.Sprintf("infisical_static_secrets_sync_failed{%s} %d", namespaceLabels, failed),
				fmt.Sprintf(`infisical_static_secret_sync_status{%s,status="synced"} 1`, healthyLabels),
				fmt.Sprintf(`infisical_static_secret_sync_status{%s,status="failed"} 0`, healthyLabels),
				fmt.Sprintf(`infisical_static_secret_sync_status{%s,status="failed"} 1`, brokenLabels),
				fmt.Sprintf("infisical_static_secret_refresh_interval_seconds{%s} 120", healthyLabels),
				fmt.Sprintf(`infisical_static_secret_info{auth_name=%q,auth_namespace=%q,instant_updates="false",%s} 1`,
					authRef.Name, authRef.Namespace, healthyLabels),
				fmt.Sprintf("infisical_static_secret_workload_redeploys_total{%s} 1", healthyLabels),
				fmt.Sprintf("infisical_static_secret_auto_reload_workloads{%s} 1", healthyLabels),
			))
			// The auto-reload count is recorded only after a complete sync, which the broken secret never had.
			g.Expect(lines).NotTo(ContainElement(HavePrefix("infisical_static_secret_auto_reload_workloads{" + brokenLabels + "}")))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("fixing the broken secret and watching it leave the failed state")
		Expect(k.Get(ctx, types.NamespacedName{Name: broken.Name, Namespace: testNamespace}, broken)).To(Succeed())
		broken.Spec.Sources[0].EnvironmentSlug = project.EnvSlug
		broken.Spec.Sources[0].SecretPath = "/metrics"
		Expect(k.Update(ctx, broken)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(scrapeMetrics(g)).To(ContainElements(
				fmt.Sprintf(`infisical_static_secret_sync_status{%s,status="synced"} 1`, brokenLabels),
				fmt.Sprintf(`infisical_static_secret_sync_status{%s,status="failed"} 0`, brokenLabels),
				fmt.Sprintf("infisical_static_secret_auto_reload_workloads{%s} 0", brokenLabels),
			))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("deleting a secret and watching its series disappear")
		Expect(k.Delete(ctx, healthy)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(scrapeMetrics(g)).NotTo(ContainElement(ContainSubstring(healthyLabels)))
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
	})
})

const operatorNamespace = "infisical-operator-system"

var _ = Describe("DefaultInfisicalConnection", Ordered, ContinueOnFailure, func() {
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

		project = api.CreateProject(GinkgoT(), "default-conn")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		identity := api.CreateIdentity(GinkgoT(), "default-conn-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds := api.SetupUniversalAuth(GinkgoT(), identity.ID)

		credentialSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-default-conn-credentials",
				Namespace: testNamespace,
			},
			StringData: map[string]string{
				"clientId":     creds.ClientID,
				"clientSecret": creds.ClientSecret,
			},
		}
		Expect(k.Create(ctx, credentialSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, credentialSecret)) })

		// Create an InfisicalAuth WITHOUT specifying infisicalConnectionRef.
		// It should automatically fall back to the "default" connection
		// installed by the Helm chart in the operator namespace.
		auth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-default-conn-auth",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				// InfisicalConnectionRef intentionally omitted to test the default fallback
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

		By("waiting for the Helm-installed default InfisicalConnection to become ready")
		Eventually(func(g Gomega) {
			var conn secretsv1beta1.InfisicalConnection
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "default", Namespace: operatorNamespace}, &conn)).To(Succeed())
			cond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalConnection has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalConnection not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())

		By("waiting for InfisicalAuth (with default connection fallback) to become ready")
		Eventually(func(g Gomega) {
			var a secretsv1beta1.InfisicalAuth
			g.Expect(k.Get(ctx, types.NamespacedName{Name: auth.Name, Namespace: auth.Namespace}, &a)).To(Succeed())
			cond := meta.FindStatusCondition(a.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalAuth has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue),
				"InfisicalAuth not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(time.Second).Should(Succeed())
	})

	It("should sync secrets using the default connection", func() {
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "DEFAULT_CONN_KEY", "default-conn-value", nil)
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/", "DEFAULT_CONN_HOST", "infisical.example.com", nil)

		ss := &secretsv1beta1.InfisicalStaticSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "e2e-default-conn-sync",
				Namespace: testNamespace,
			},
			Spec: secretsv1beta1.InfisicalStaticSecretSpec{
				InfisicalAuthRef: authRef,
				SyncOptions:      &secretsv1beta1.SyncOptions{RefreshInterval: "1h"},
				Sources: []secretsv1beta1.SecretSource{{
					ProjectId:       project.ID,
					EnvironmentSlug: project.EnvSlug,
					SecretPath:      "/",
				}},
				Targets: []secretsv1beta1.SecretTarget{{
					Name:           "e2e-default-conn-synced",
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
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-default-conn-synced", Namespace: testNamespace}, &synced)).To(Succeed())
			g.Expect(synced.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())

		Expect(synced.Data).To(HaveKeyWithValue("DEFAULT_CONN_KEY", []byte("default-conn-value")))
		Expect(synced.Data).To(HaveKeyWithValue("DEFAULT_CONN_HOST", []byte("infisical.example.com")))
	})
})

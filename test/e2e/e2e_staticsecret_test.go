package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
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

	expectSecret := func(name string) *corev1.Secret {
		GinkgoHelper()
		var secret corev1.Secret
		Eventually(func(g Gomega) {
			g.Expect(k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &secret)).To(Succeed())
			g.Expect(secret.Data).NotTo(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(time.Second).Should(Succeed())
		return &secret
	}

	expectConfigMap := func(name string) *corev1.ConfigMap {
		GinkgoHelper()
		var cm corev1.ConfigMap
		Eventually(func(g Gomega) {
			g.Expect(k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &cm)).To(Succeed())
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

		synced := expectSecret("e2e-basic-synced")
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

		synced := expectSecret("e2e-folder-synced")
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

		synced := expectSecret("e2e-recursive-synced")
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

		synced := expectSecret("e2e-multisource-synced")
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

		cm := expectConfigMap("e2e-configmap-synced")
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

		synced := expectSecret("e2e-template-synced")
		expectSecretData(synced, map[string]string{
			"DSN": fmt.Sprintf("postgres://%s:%s@%s/%s", tplUser, tplPass, tplHost, tplDB),
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

		synced := expectSecret("e2e-metadata-synced")
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

		synced := expectSecret("e2e-mt-secret")
		expectSecretData(synced, map[string]string{"SHARED_KEY": "shared-val"})

		cm := expectConfigMap("e2e-mt-configmap")
		Expect(cm.Data).To(HaveKeyWithValue("SHARED_KEY", "shared-val"))
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

		synced := expectSecret("e2e-import-synced")
		expectSecretData(synced, map[string]string{
			"APP_NAME":  "my-app",
			"REDIS_URL": "redis://cache:6379",
		})
	})
})

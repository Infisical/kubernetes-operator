package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
)

const testNamespace = "default"

func TestStaticSecret(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "true" {
		t.Skip()
		return
	}

	ctx := context.Background()
	api := testInfra.NodeJS()
	k := testManager.Client()

	project := api.CreateProject(t, "static-secret")
	t.Cleanup(func() { api.DeleteProject(t, project.ID) })

	identity := api.CreateIdentity(t, "test-identity")
	t.Cleanup(func() { api.DeleteIdentity(t, identity.ID) })

	api.AddIdentityToProject(t, project.ID, identity.ID, infra.Role("admin"))
	creds := api.SetupUniversalAuth(t, identity.ID)

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
	mustCreate(t, k, ctx, credentialSecret)
	t.Cleanup(func() { mustDelete(t, k, ctx, credentialSecret) })

	connection := &secretsv1beta1.InfisicalConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e-connection",
			Namespace: testNamespace,
		},
		Spec: secretsv1beta1.InfisicalConnectionSpec{
			Address: api.URL(),
		},
	}
	mustCreate(t, k, ctx, connection)
	t.Cleanup(func() { mustDelete(t, k, ctx, connection) })

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
	mustCreate(t, k, ctx, auth)
	t.Cleanup(func() { mustDelete(t, k, ctx, auth) })

	authRef := secretsv1beta1.NamespacedName{
		Name:      auth.Name,
		Namespace: auth.Namespace,
	}

	t.Run("basic sync", func(t *testing.T) {
		api.CreateSecret(t, project.ID, project.EnvSlug, "/", "DB_HOST", "localhost", nil)
		api.CreateSecret(t, project.ID, project.EnvSlug, "/", "DB_PORT", "5432", nil)
		api.CreateSecret(t, project.ID, project.EnvSlug, "/", "API_KEY", "super-secret-key", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-basic-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-basic-synced")

		assertSecretData(t, synced, map[string]string{
			"DB_HOST": "localhost",
			"DB_PORT": "5432",
			"API_KEY": "super-secret-key",
		})
	})

	t.Run("nested folder", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "database")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/database", "HOST", "db.internal", nil)
		api.CreateSecret(t, project.ID, project.EnvSlug, "/database", "PORT", "3306", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-folder-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-folder-synced")

		assertSecretData(t, synced, map[string]string{
			"HOST": "db.internal",
			"PORT": "3306",
		})
	})

	t.Run("recursive", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "services")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/services", "ROOT_KEY", "root-val", nil)
		api.CreateFolder(t, project.ID, project.EnvSlug, "/services", "auth")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/services/auth", "JWT_SECRET", "jwt-val", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-recursive-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-recursive-synced")

		assertSecretData(t, synced, map[string]string{
			"ROOT_KEY":   "root-val",
			"JWT_SECRET": "jwt-val",
		})
	})

	t.Run("multiple sources", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "frontend")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/frontend", "NEXT_PUBLIC_URL", "https://app.test", nil)

		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "backend")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/backend", "INTERNAL_URL", "http://api.internal", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-multisource-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-multisource-synced")

		assertSecretData(t, synced, map[string]string{
			"NEXT_PUBLIC_URL": "https://app.test",
			"INTERNAL_URL":    "http://api.internal",
		})
	})

	t.Run("configmap target", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "config")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/config", "LOG_LEVEL", "debug", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-configmap-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		cm := waitForConfigMap(t, k, ctx, "e2e-configmap-synced")

		if got, want := cm.Data["LOG_LEVEL"], "debug"; got != want {
			t.Errorf("configmap LOG_LEVEL = %q, want %q", got, want)
		}
	})

	t.Run("templated secret", func(t *testing.T) {
		rnd := make([]byte, 4)
		rand.Read(rnd)
		suffix := hex.EncodeToString(rnd)
		tplUser := "user" + suffix
		tplPass := "pass" + suffix
		tplHost := "host-" + suffix + ".test"
		tplDB := "db" + suffix

		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "templated")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/templated", "PG_USER", tplUser, nil)
		api.CreateSecret(t, project.ID, project.EnvSlug, "/templated", "PG_PASS", tplPass, nil)
		api.CreateSecret(t, project.ID, project.EnvSlug, "/templated", "PG_HOST", tplHost, nil)
		api.CreateSecret(t, project.ID, project.EnvSlug, "/templated", "PG_DB", tplDB, nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-template-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
					Data: map[string]string{
						"DSN": "postgres://{{ .PG_USER.Value }}:{{ .PG_PASS.Value }}@{{ .PG_HOST.Value }}/{{ .PG_DB.Value }}",
					},
				},
			}},
		})
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-template-synced")

		assertSecretData(t, synced, map[string]string{
			"DSN": fmt.Sprintf("postgres://%s:%s@%s/%s", tplUser, tplPass, tplHost, tplDB),
		})
	})

	t.Run("target metadata", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "labeled")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/labeled", "TOKEN", "abc123", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-metadata-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-metadata-synced")

		if got, want := synced.Annotations["infisical.com/env"], "test"; got != want {
			t.Errorf("annotation infisical.com/env = %q, want %q", got, want)
		}
		if got, want := synced.Labels["app"], "e2e"; got != want {
			t.Errorf("label app = %q, want %q", got, want)
		}
		assertSecretData(t, synced, map[string]string{"TOKEN": "abc123"})
	})

	t.Run("multiple targets", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "multitarget")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/multitarget", "SHARED_KEY", "shared-val", nil)

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-multitarget-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-mt-secret")
		assertSecretData(t, synced, map[string]string{"SHARED_KEY": "shared-val"})

		cm := waitForConfigMap(t, k, ctx, "e2e-mt-configmap")
		if got, want := cm.Data["SHARED_KEY"], "shared-val"; got != want {
			t.Errorf("configmap SHARED_KEY = %q, want %q", got, want)
		}
	})

	t.Run("secret import", func(t *testing.T) {
		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "shared-lib")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/shared-lib", "REDIS_URL", "redis://cache:6379", nil)

		api.CreateFolder(t, project.ID, project.EnvSlug, "/", "app-with-import")
		api.CreateSecret(t, project.ID, project.EnvSlug, "/app-with-import", "APP_NAME", "my-app", nil)
		api.CreateSecretImport(t, project.ID, project.EnvSlug, "/app-with-import", project.EnvSlug, "/shared-lib")

		ss := mustCreateStaticSecret(t, k, ctx, "e2e-import-sync", secretsv1beta1.InfisicalStaticSecretSpec{
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
		t.Cleanup(func() { mustDelete(t, k, ctx, ss) })

		synced := waitForSecret(t, k, ctx, "e2e-import-synced")

		assertSecretData(t, synced, map[string]string{
			"APP_NAME":  "my-app",
			"REDIS_URL": "redis://cache:6379",
		})
	})
}

func TestMetricsEndpoint(t *testing.T) {
	if os.Getenv("INTEGRATION_TESTS") != "true" {
		t.Skip()
		return
	}

	addr := testManager.MetricsAddress()
	resp, err := http.Get(fmt.Sprintf("http://%s/metrics", addr))
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /metrics returned %d, want 200", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	metricsBody := string(body)

	expectedMetrics := []string{
		"controller_runtime_reconcile_total",
		"controller_runtime_reconcile_errors_total",
		"workqueue_adds_total",
	}
	for _, metric := range expectedMetrics {
		if !strings.Contains(metricsBody, metric) {
			t.Errorf("metrics output missing %q", metric)
		}
	}
}

func mustCreate(t *testing.T, k client.Client, ctx context.Context, obj client.Object) {
	t.Helper()
	if err := k.Create(ctx, obj); err != nil {
		t.Fatalf("create %T %q: %v", obj, obj.GetName(), err)
	}
}

func mustDelete(t *testing.T, k client.Client, ctx context.Context, obj client.Object) {
	t.Helper()
	if err := k.Delete(ctx, obj); client.IgnoreNotFound(err) != nil {
		t.Logf("cleanup %T %q: %v", obj, obj.GetName(), err)
	}
}

func mustCreateStaticSecret(t *testing.T, k client.Client, ctx context.Context, name string, spec secretsv1beta1.InfisicalStaticSecretSpec) *secretsv1beta1.InfisicalStaticSecret {
	t.Helper()
	ss := &secretsv1beta1.InfisicalStaticSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: spec,
	}
	if err := k.Create(ctx, ss); err != nil {
		t.Fatalf("create InfisicalStaticSecret %q: %v", name, err)
	}
	return ss
}

func waitForSecret(t *testing.T, k client.Client, ctx context.Context, name string) corev1.Secret {
	t.Helper()
	var secret corev1.Secret
	waitFor(t, 30*time.Second, time.Second, func() bool {
		err := k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &secret)
		return err == nil && len(secret.Data) > 0
	})
	return secret
}

func waitForConfigMap(t *testing.T, k client.Client, ctx context.Context, name string) corev1.ConfigMap {
	t.Helper()
	var cm corev1.ConfigMap
	waitFor(t, 30*time.Second, time.Second, func() bool {
		err := k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &cm)
		return err == nil && len(cm.Data) > 0
	})
	return cm
}

func waitFor(t *testing.T, timeout, interval time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(interval)
	}
}

func assertSecretData(t *testing.T, secret corev1.Secret, expected map[string]string) {
	t.Helper()
	for key, want := range expected {
		got := string(secret.Data[key])
		if got != want {
			t.Errorf("secret data %q = %q, want %q", key, got, want)
		}
	}
}

package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/infra"
)

var _ = Describe("InfisicalStaticSecretPush", Ordered, ContinueOnFailure, func() {
	const (
		pushTimeout  = 30 * time.Second
		pollInterval = time.Second
		otherEnvSlug = "prod"
	)

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

		project = api.CreateProject(GinkgoT(), "static-secret-push")
		DeferCleanup(func() { api.DeleteProject(GinkgoT(), project.ID) })

		identity := api.CreateIdentity(GinkgoT(), "push-identity")
		DeferCleanup(func() { api.DeleteIdentity(GinkgoT(), identity.ID) })

		api.AddIdentityToProject(GinkgoT(), project.ID, identity.ID, infra.Role("admin"))
		creds := api.SetupUniversalAuth(GinkgoT(), identity.ID)

		credentialSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-push-ua-credentials", Namespace: testNamespace},
			StringData: map[string]string{"clientId": creds.ClientID, "clientSecret": creds.ClientSecret},
		}
		Expect(k.Create(ctx, credentialSecret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, credentialSecret)) })

		connection := &secretsv1beta1.InfisicalConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-push-connection", Namespace: testNamespace},
			Spec:       secretsv1beta1.InfisicalConnectionSpec{Address: testManager.InClusterAPIURL()},
		}
		Expect(k.Create(ctx, connection)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, connection)) })

		infisicalAuth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "e2e-push-auth", Namespace: testNamespace},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: secretsv1beta1.NamespacedName{Name: connection.Name, Namespace: connection.Namespace},
				Method:                 secretsv1beta1.UniversalAuth,
				Universal: &secretsv1beta1.UniversalAuthConfig{
					ClientIdRef:     secretsv1beta1.SecretReference{Name: credentialSecret.Name, Namespace: testNamespace, Key: "clientId"},
					ClientSecretRef: secretsv1beta1.SecretReference{Name: credentialSecret.Name, Namespace: testNamespace, Key: "clientSecret"},
				},
			},
		}
		Expect(k.Create(ctx, infisicalAuth)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, infisicalAuth)) })
		authRef = secretsv1beta1.NamespacedName{Name: infisicalAuth.Name, Namespace: testNamespace}

		By("waiting for InfisicalAuth to become ready")
		Eventually(func(g Gomega) {
			var a secretsv1beta1.InfisicalAuth
			g.Expect(k.Get(ctx, types.NamespacedName{Name: infisicalAuth.Name, Namespace: testNamespace}, &a)).To(Succeed())
			cond := meta.FindStatusCondition(a.Status.Conditions, "secrets.infisical.com/IsReady")
			g.Expect(cond).NotTo(BeNil(), "InfisicalAuth has no IsReady condition yet")
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "InfisicalAuth not ready: %s", cond.Message)
		}).WithTimeout(60 * time.Second).WithPolling(pollInterval).Should(Succeed())
	})

	createSource := func(name string, data map[string]string) *corev1.Secret {
		GinkgoHelper()
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			StringData: data,
		}
		Expect(k.Create(ctx, secret)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, secret)) })
		return secret
	}

	updateSource := func(name string, data map[string]string) {
		GinkgoHelper()
		var secret corev1.Secret
		Expect(k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &secret)).To(Succeed())
		secret.Data = map[string][]byte{}
		for key, value := range data {
			secret.Data[key] = []byte(value)
		}
		Expect(k.Update(ctx, &secret)).To(Succeed())
	}

	source := func(names ...string) secretsv1beta1.PushSources {
		sources := secretsv1beta1.PushSources{}
		for _, name := range names {
			sources.Secrets = append(sources.Secrets, secretsv1beta1.NamespacedName{Name: name, Namespace: testNamespace})
		}
		return sources
	}

	target := func(envSlug, secretPath string) secretsv1beta1.PushTarget {
		return secretsv1beta1.PushTarget{ProjectId: project.ID, EnvironmentSlug: envSlug, SecretPath: secretPath}
	}

	createPush := func(name string, spec secretsv1beta1.InfisicalStaticSecretPushSpec) {
		GinkgoHelper()
		spec.InfisicalAuthRef = authRef
		push := &secretsv1beta1.InfisicalStaticSecretPush{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec:       spec,
		}
		Expect(k.Create(ctx, push)).To(Succeed())
		DeferCleanup(func() { _ = client.IgnoreNotFound(k.Delete(ctx, push)) })
	}

	updatePush := func(name string, mutate func(*secretsv1beta1.InfisicalStaticSecretPushSpec)) {
		GinkgoHelper()
		var push secretsv1beta1.InfisicalStaticSecretPush
		Expect(k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &push)).To(Succeed())
		mutate(&push.Spec)
		Expect(k.Update(ctx, &push)).To(Succeed())
	}

	deletePush := func(name string) {
		GinkgoHelper()
		var push secretsv1beta1.InfisicalStaticSecretPush
		Expect(k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &push)).To(Succeed())
		Expect(k.Delete(ctx, &push)).To(Succeed())
		Eventually(func() bool {
			err := k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &secretsv1beta1.InfisicalStaticSecretPush{})
			return k8sErrors.IsNotFound(err)
		}).WithTimeout(pushTimeout).WithPolling(pollInterval).Should(BeTrue())
	}

	// expectSynced waits until the latest spec was pushed successfully.
	expectSynced := func(name string) {
		GinkgoHelper()
		Eventually(func(g Gomega) {
			var push secretsv1beta1.InfisicalStaticSecretPush
			g.Expect(k.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, &push)).To(Succeed())
			cond := meta.FindStatusCondition(push.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(push.Status.ObservedGeneration).To(Equal(push.Generation))
			g.Expect(cond.Status).To(Equal(metav1.ConditionTrue), "InfisicalStaticSecretPush %q failed: %s", name, cond.Message)
		}).WithTimeout(pushTimeout).WithPolling(pollInterval).Should(Succeed())
	}

	expectPushed := func(envSlug, secretPath string, expected map[string]string) {
		GinkgoHelper()
		Eventually(func() map[string]string {
			return api.ListSecrets(GinkgoT(), project.ID, envSlug, secretPath)
		}).WithTimeout(pushTimeout).WithPolling(pollInterval).Should(Equal(expected))
	}

	It("should push a Secret to a folder that doesn't exist yet", func() {
		createSource("e2e-push-basic-source", map[string]string{"DB_PASSWORD": "secret", "API_KEY": "key"})

		createPush("e2e-push-basic", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-basic-source"),
			Targets: []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-basic/nested")},
		})

		expectSynced("e2e-push-basic")
		expectPushed(project.EnvSlug, "/push-basic/nested", map[string]string{"DB_PASSWORD": "secret", "API_KEY": "key"})
	})

	It("should merge several sources, the first one winning, and push them to several targets", func() {
		createSource("e2e-push-merge-first", map[string]string{"SHARED": "first", "ONLY_FIRST": "1"})
		createSource("e2e-push-merge-second", map[string]string{"SHARED": "second", "ONLY_SECOND": "2"})

		createPush("e2e-push-merge", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-merge-first", "e2e-push-merge-second"),
			Targets: []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-merge"), target(otherEnvSlug, "/push-merge")},
		})

		expected := map[string]string{"SHARED": "first", "ONLY_FIRST": "1", "ONLY_SECOND": "2"}
		expectSynced("e2e-push-merge")
		expectPushed(project.EnvSlug, "/push-merge", expected)
		expectPushed(otherEnvSlug, "/push-merge", expected)
	})

	It("should push what a target's template renders instead of the source keys", func() {
		createSource("e2e-push-template-source", map[string]string{"DB_USER": "admin", "DB_PASS": "secret"})

		templated := target(project.EnvSlug, "/push-template")
		templated.Template = &secretsv1beta1.SecretTemplate{EngineVersion: "v1", Data: secretsv1beta1.SecretTemplateData{
			Map: map[string]string{"DATABASE_URL": "postgres://{{ .DB_USER.Value }}:{{ .DB_PASS.Value | upper }}@db:5432"},
		}}
		createPush("e2e-push-template", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-template-source"),
			Targets: []secretsv1beta1.PushTarget{templated},
		})

		expectSynced("e2e-push-template")
		expectPushed(project.EnvSlug, "/push-template", map[string]string{"DATABASE_URL": "postgres://admin:SECRET@db:5432"})
	})

	It("should leave existing keys untouched with updatePolicy None", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "push-none")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/push-none", "EXISTING", "manual", nil)
		createSource("e2e-push-none-source", map[string]string{"EXISTING": "pushed", "NEW": "value"})

		createPush("e2e-push-none", secretsv1beta1.InfisicalStaticSecretPushSpec{
			SyncOptions: secretsv1beta1.PushSyncOptions{UpdatePolicy: secretsv1beta1.PushUpdatePolicyNone},
			Sources:     source("e2e-push-none-source"),
			Targets:     []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-none")},
		})

		expectSynced("e2e-push-none")
		expectPushed(project.EnvSlug, "/push-none", map[string]string{"EXISTING": "manual", "NEW": "value"})
	})

	It("should take over existing keys with updatePolicy Replace", func() {
		api.CreateFolder(GinkgoT(), project.ID, project.EnvSlug, "/", "push-replace")
		api.CreateSecret(GinkgoT(), project.ID, project.EnvSlug, "/push-replace", "EXISTING", "manual", nil)
		createSource("e2e-push-replace-source", map[string]string{"EXISTING": "pushed"})

		createPush("e2e-push-replace", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-replace-source"),
			Targets: []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-replace")},
		})

		expectSynced("e2e-push-replace")
		expectPushed(project.EnvSlug, "/push-replace", map[string]string{"EXISTING": "pushed"})

		By("deleting the taken over key together with the resource")
		deletePush("e2e-push-replace")
		expectPushed(project.EnvSlug, "/push-replace", map[string]string{})
	})

	It("should push changes and delete removed keys when the source Secret changes", func() {
		createSource("e2e-push-update-source", map[string]string{"KEEP": "old", "REMOVE": "value"})

		createPush("e2e-push-update", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-update-source"),
			Targets: []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-update")},
		})
		expectSynced("e2e-push-update")
		expectPushed(project.EnvSlug, "/push-update", map[string]string{"KEEP": "old", "REMOVE": "value"})

		updateSource("e2e-push-update-source", map[string]string{"KEEP": "new", "ADDED": "value"})

		expectPushed(project.EnvSlug, "/push-update", map[string]string{"KEEP": "new", "ADDED": "value"})
	})

	It("should delete the secrets of a removed target", func() {
		createSource("e2e-push-target-source", map[string]string{"KEY": "value"})

		createPush("e2e-push-target", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-target-source"),
			Targets: []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-target-kept"), target(project.EnvSlug, "/push-target-removed")},
		})
		expectSynced("e2e-push-target")
		expectPushed(project.EnvSlug, "/push-target-removed", map[string]string{"KEY": "value"})

		updatePush("e2e-push-target", func(spec *secretsv1beta1.InfisicalStaticSecretPushSpec) {
			spec.Targets = spec.Targets[:1]
		})

		expectSynced("e2e-push-target")
		expectPushed(project.EnvSlug, "/push-target-removed", map[string]string{})
		expectPushed(project.EnvSlug, "/push-target-kept", map[string]string{"KEY": "value"})
	})

	It("should keep the pushed secrets when deleted with deletionPolicy None", func() {
		createSource("e2e-push-retain-source", map[string]string{"KEY": "value"})

		createPush("e2e-push-retain", secretsv1beta1.InfisicalStaticSecretPushSpec{
			SyncOptions: secretsv1beta1.PushSyncOptions{DeletionPolicy: secretsv1beta1.PushDeletionPolicyNone},
			Sources:     source("e2e-push-retain-source"),
			Targets:     []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-retain")},
		})
		expectSynced("e2e-push-retain")
		expectPushed(project.EnvSlug, "/push-retain", map[string]string{"KEY": "value"})

		deletePush("e2e-push-retain")

		Consistently(func() map[string]string {
			return api.ListSecrets(GinkgoT(), project.ID, project.EnvSlug, "/push-retain")
		}).WithTimeout(5 * time.Second).WithPolling(pollInterval).Should(Equal(map[string]string{"KEY": "value"}))
	})

	It("should report a missing source Secret without pushing anything", func() {
		createPush("e2e-push-missing", secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: source("e2e-push-missing-source"),
			Targets: []secretsv1beta1.PushTarget{target(project.EnvSlug, "/push-missing")},
		})

		Eventually(func(g Gomega) {
			var push secretsv1beta1.InfisicalStaticSecretPush
			g.Expect(k.Get(ctx, types.NamespacedName{Name: "e2e-push-missing", Namespace: testNamespace}, &push)).To(Succeed())
			cond := meta.FindStatusCondition(push.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Message).To(ContainSubstring("source Secret default/e2e-push-missing-source not found"))
			g.Expect(push.Status.Error).To(BeTrue())
		}).WithTimeout(pushTimeout).WithPolling(pollInterval).Should(Succeed())
		Expect(api.ListSecrets(GinkgoT(), project.ID, project.EnvSlug, "/push-missing")).To(BeEmpty())
	})
})

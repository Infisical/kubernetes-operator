package infisicalstaticsecretpush_test

import (
	"context"
	"maps"
	"time"

	"github.com/go-logr/logr"
	infisicalSdk "github.com/infisical/go-sdk"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/conditions"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	svc "github.com/Infisical/infisical/k8-operator/internal/services/infisicalstaticsecretpush"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/fakeinfisical"
)

const (
	projectID = "project-1"
	namespace = "default"
	upsertReq = "PATCH /api/v4/secrets/batch"
	deleteReq = "DELETE /api/v4/secrets/batch"
)

type fakeAuthStrategy struct{}

func (fakeAuthStrategy) Validate(context.Context, *v1beta1.InfisicalAuth) error { return nil }

func (fakeAuthStrategy) Authenticate(context.Context, *model.InfisicalConnection, *v1beta1.InfisicalAuth) (*model.AuthenticationResult, error) {
	return &model.AuthenticationResult{
		MachineIdentity: infisicalSdk.MachineIdentityCredential{AccessToken: "token", ExpiresIn: 3600},
	}, nil
}

type testEnv struct {
	ctx       context.Context
	infisical *fakeinfisical.Server
	client    client.Client
	handler   *svc.InfisicalStaticSecretPushHandler
}

func newTestEnv(objects ...client.Object) *testEnv {
	server := fakeinfisical.New([]string{projectID}, []string{"dev", "prod"})
	DeferCleanup(server.Close)

	ready := []metav1.Condition{{
		Type:               conditions.IsReady,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		LastTransitionTime: metav1.Now(),
	}}
	connection := &v1beta1.InfisicalConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "connection", Namespace: namespace},
		Spec:       v1beta1.InfisicalConnectionSpec{Address: server.URL},
		Status:     v1beta1.InfisicalConnectionStatus{Conditions: ready},
	}
	infisicalAuth := &v1beta1.InfisicalAuth{
		ObjectMeta: metav1.ObjectMeta{Name: "auth", Namespace: namespace},
		Spec: v1beta1.InfisicalAuthSpec{
			InfisicalConnectionRef: v1beta1.NamespacedName{Name: connection.Name, Namespace: namespace},
			Method:                 v1beta1.UniversalAuth,
		},
		Status: v1beta1.InfisicalAuthStatus{Conditions: ready},
	}

	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	Expect(v1beta1.AddToScheme(scheme)).To(Succeed())

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(append(objects, connection, infisicalAuth)...).
		WithStatusSubresource(&v1beta1.InfisicalStaticSecretPush{}).
		WithInterceptorFuncs(interceptor.Funcs{
			// The fake client ignores the context, but a real one fails once it has ended.
			SubResourcePatch: func(ctx context.Context, c client.Client, subResource string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				return c.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	authCache, err := cache.NewAuthCache()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(authCache.Cleanup)
	resolver := auth.NewAuthStrategyResolverForTesting(k8sClient, authCache, map[v1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{
		v1beta1.UniversalAuth: fakeAuthStrategy{},
	})

	return &testEnv{
		ctx:       context.Background(),
		infisical: server,
		client:    k8sClient,
		handler:   svc.NewInfisicalStaticSecretPushHandler(k8sClient, false, resolver, nil, logr.Discard()),
	}
}

func sourceSecret(name string, data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{},
	}
	for key, value := range data {
		secret.Data[key] = []byte(value)
	}
	return secret
}

func target(environment, secretPath string) v1beta1.PushTarget {
	return v1beta1.PushTarget{ProjectId: projectID, EnvironmentSlug: environment, SecretPath: secretPath}
}

// createPush stores a push resource with defaulted sync options, as the API server would.
func (e *testEnv) createPush(sources []string, targets []v1beta1.PushTarget, mutate ...func(*v1beta1.InfisicalStaticSecretPushSpec)) *v1beta1.InfisicalStaticSecretPush {
	return e.createNamedPush("push", sources, targets, mutate...)
}

func (e *testEnv) createNamedPush(name string, sources []string, targets []v1beta1.PushTarget, mutate ...func(*v1beta1.InfisicalStaticSecretPushSpec)) *v1beta1.InfisicalStaticSecretPush {
	push := &v1beta1.InfisicalStaticSecretPush{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1beta1.InfisicalStaticSecretPushSpec{
			InfisicalAuthRef: v1beta1.NamespacedName{Name: "auth", Namespace: namespace},
			SyncOptions: v1beta1.PushSyncOptions{
				UpdatePolicy:         v1beta1.PushUpdatePolicyReplace,
				DeletionPolicy:       v1beta1.PushDeletionPolicyDelete,
				CreateMissingFolders: ptr.To(true),
			},
			Targets: targets,
		},
	}
	for _, source := range sources {
		push.Spec.Sources.Secrets = append(push.Spec.Sources.Secrets, v1beta1.NamespacedName{Name: source, Namespace: namespace})
	}
	for _, fn := range mutate {
		fn(&push.Spec)
	}

	Expect(e.client.Create(e.ctx, push)).To(Succeed())
	return e.get(push)
}

func (e *testEnv) get(push *v1beta1.InfisicalStaticSecretPush) *v1beta1.InfisicalStaticSecretPush {
	latest := &v1beta1.InfisicalStaticSecretPush{}
	Expect(e.client.Get(e.ctx, client.ObjectKeyFromObject(push), latest)).To(Succeed())
	return latest
}

func (e *testEnv) update(obj client.Object) {
	Expect(e.client.Update(e.ctx, obj)).To(Succeed())
}

// sync runs a sync on the stored resource and returns the stored resource afterwards.
func (e *testEnv) sync(push *v1beta1.InfisicalStaticSecretPush) (*v1beta1.InfisicalStaticSecretPush, error) {
	current := e.get(push)
	_, err := e.handler.SyncSecrets(e.ctx, current)
	return e.get(push), err
}

func (e *testEnv) mustSync(push *v1beta1.InfisicalStaticSecretPush) *v1beta1.InfisicalStaticSecretPush {
	synced, err := e.sync(push)
	Expect(err).NotTo(HaveOccurred())
	return synced
}

func (e *testEnv) values(environment, secretPath string) map[string]string {
	values := map[string]string{}
	for key, secret := range e.infisical.Secrets(projectID, environment, secretPath) {
		values[key] = secret.Value
	}
	return values
}

func (e *testEnv) ids(environment, secretPath string) map[string]string {
	ids := map[string]string{}
	for key, secret := range e.infisical.Secrets(projectID, environment, secretPath) {
		ids[secret.ID] = key
	}
	return ids
}

func reconcileStatus(push *v1beta1.InfisicalStaticSecretPush) *metav1.Condition {
	return meta.FindStatusCondition(push.Status.Conditions, svc.ConditionLastReconcileStatus)
}

func mergeMaps(all ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, m := range all {
		maps.Copy(merged, m)
	}
	return merged
}

var _ = Describe("SyncSecrets", func() {
	It("pushes every key of every source to every target, the first source winning", func() {
		env := newTestEnv(
			sourceSecret("db", map[string]string{"DB_PASSWORD": "secret", "SHARED": "from-db"}),
			sourceSecret("api", map[string]string{"API_KEY": "key", "SHARED": "from-api"}),
		)
		push := env.createPush([]string{"db", "api"}, []v1beta1.PushTarget{target("dev", "/"), target("prod", "/app")})

		push = env.mustSync(push)

		expected := map[string]string{"DB_PASSWORD": "secret", "API_KEY": "key", "SHARED": "from-db"}
		Expect(env.values("dev", "/")).To(Equal(expected))
		Expect(env.values("prod", "/app")).To(Equal(expected))
		Expect(push.Status.ManagedSecrets).To(Equal(mergeMaps(env.ids("dev", "/"), env.ids("prod", "/app"))))
		Expect(push.Status.Error).To(BeFalse())
		Expect(reconcileStatus(push).Status).To(Equal(metav1.ConditionTrue))
		Expect(meta.FindStatusCondition(push.Status.Conditions, svc.ConditionLastReconcileAuthMethod).Message).To(Equal("universal"))
		Expect(meta.FindStatusCondition(push.Status.Conditions, svc.ConditionLastSuccessfulReconcileAt)).NotTo(BeNil())
	})

	It("doesn't write to Infisical when nothing changed", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value", "PADDED": "  value \n\n"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		env.mustSync(push)
		env.infisical.ResetRequests()

		env.mustSync(push)

		Expect(env.infisical.CountRequests(upsertReq)).To(Equal(0))
		Expect(env.infisical.CountRequests(deleteReq)).To(Equal(0))
	})

	It("pushes changed values", func() {
		source := sourceSecret("source", map[string]string{"KEY": "value"})
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		push = env.mustSync(push)
		id := env.infisical.Secrets(projectID, "dev", "/")["KEY"].ID

		source.Data["KEY"] = []byte("changed")
		env.update(source)
		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "changed"}))
		Expect(push.Status.ManagedSecrets).To(Equal(map[string]string{id: "KEY"}))
	})

	It("creates a missing folder and its parents", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/a/b/c/")})

		env.mustSync(push)

		Expect(env.infisical.FolderExists(projectID, "dev", "/a/b/c")).To(BeTrue())
		Expect(env.values("dev", "/a/b/c")).To(Equal(map[string]string{"KEY": "value"}))
	})

	It("fails the target when the folder is missing and createMissingFolders is false", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/missing")}, func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.CreateMissingFolders = ptr.To(false)
		})

		push, err := env.sync(push)

		Expect(err).To(MatchError(ContainSubstring("createMissingFolders")))
		Expect(env.infisical.FolderExists(projectID, "dev", "/missing")).To(BeFalse())
		Expect(push.Status.Error).To(BeTrue())
		Expect(reconcileStatus(push).Status).To(Equal(metav1.ConditionFalse))
		Expect(reconcileStatus(push).Message).To(ContainSubstring("targets[0] (project project-1, dev:/missing)"))
	})

	It("leaves existing keys untouched with updatePolicy None", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"EXISTING": "pushed", "NEW": "value"}))
		existingID := env.infisical.CreateSecret(projectID, "dev", "/", "EXISTING", "manual")
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")}, func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.UpdatePolicy = v1beta1.PushUpdatePolicyNone
		})

		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"EXISTING": "manual", "NEW": "value"}))
		Expect(push.Status.ManagedSecrets).NotTo(HaveKey(existingID))
		Expect(push.Status.ManagedSecrets).To(HaveLen(1))
	})

	It("never changes a key that exists with updatePolicy None, including keys it pushed", func() {
		source := sourceSecret("source", map[string]string{"KEY": "value", "REMOVED": "value"})
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")}, func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.UpdatePolicy = v1beta1.PushUpdatePolicyNone
		})
		env.mustSync(push)

		source.Data["KEY"] = []byte("changed")
		env.update(source)
		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value", "REMOVED": "value"}))
		Expect(push.Status.ManagedSecrets).To(HaveLen(2))

		By("still creating the key again when it is deleted from Infisical")
		env.infisical.DeleteSecret(env.infisical.Secrets(projectID, "dev", "/")["KEY"].ID)
		env.mustSync(push)
		Expect(env.values("dev", "/")).To(HaveKeyWithValue("KEY", "changed"))

		By("still deleting a key it pushed once it is removed from the sources")
		delete(source.Data, "REMOVED")
		env.update(source)
		env.mustSync(push)
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "changed"}))
	})

	It("leaves keys another resource pushed to it, and reports them", func() {
		env := newTestEnv(
			sourceSecret("first-source", map[string]string{"SHARED": "first"}),
			sourceSecret("second-source", map[string]string{"SHARED": "second", "OWN": "value"}),
		)
		first := env.createNamedPush("first", []string{"first-source"}, []v1beta1.PushTarget{target("dev", "/")})
		env.mustSync(first)
		second := env.createNamedPush("second", []string{"second-source"}, []v1beta1.PushTarget{target("dev", "/")})

		second, err := env.sync(second)

		Expect(err).To(MatchError(ContainSubstring("SHARED (pushed by default/first)")))
		Expect(reconcileStatus(second).Message).To(ContainSubstring("push each key from only one resource"))
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"SHARED": "first", "OWN": "value"}))
		Expect(second.Status.ManagedSecrets).To(Equal(map[string]string{env.infisical.Secrets(projectID, "dev", "/")["OWN"].ID: "OWN"}))

		By("not deleting the shared key when the second resource is deleted")
		Expect(env.handler.DeleteManagedSecrets(env.ctx, second)).To(Succeed())
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"SHARED": "first"}))
	})

	It("doesn't delete a key that another resource also recorded", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		first := env.createNamedPush("first", []string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		first = env.mustSync(first)
		// Two resources can record the same secret when they create it at the same time.
		second := env.createNamedPush("second", []string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		second.Status.ManagedSecrets = first.Status.ManagedSecrets
		Expect(env.client.Status().Update(env.ctx, second)).To(Succeed())

		Expect(env.handler.DeleteManagedSecrets(env.ctx, env.get(first))).To(Succeed())

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
		Expect(env.get(first).Status.ManagedSecrets).To(BeEmpty())
	})

	It("doesn't take over a key whose owner is being deleted until that owner is gone", func() {
		env := newTestEnv(
			sourceSecret("first-source", map[string]string{"SHARED": "first"}),
			sourceSecret("second-source", map[string]string{"SHARED": "second"}),
		)
		first := env.mustSync(env.createNamedPush("first", []string{"first-source"}, []v1beta1.PushTarget{target("dev", "/")}))
		// The finalizer keeps the first resource around while its cleanup is pending.
		first.Finalizers = []string{"test/keep"}
		env.update(first)
		Expect(env.client.Delete(env.ctx, env.get(first))).To(Succeed())
		second := env.createNamedPush("second", []string{"second-source"}, []v1beta1.PushTarget{target("dev", "/")})

		_, err := env.sync(second)

		Expect(err).To(MatchError(ContainSubstring("SHARED (pushed by default/first, which is being deleted)")))
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"SHARED": "first"}))
		Expect(env.get(second).Status.ManagedSecrets).To(BeEmpty())
	})

	It("doesn't delete a key a live resource recorded when other owners are being deleted", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		cleaning := env.mustSync(env.createNamedPush("a-cleaning", []string{"source"}, []v1beta1.PushTarget{target("dev", "/")}))
		// Three resources can record the same secret when they create it at the same time.
		// The live one is listed before the other one being deleted.
		for _, name := range []string{"b-live", "c-deleting"} {
			other := env.createNamedPush(name, []string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
			other.Status.ManagedSecrets = cleaning.Status.ManagedSecrets
			Expect(env.client.Status().Update(env.ctx, other)).To(Succeed())
		}
		deleting := env.get(&v1beta1.InfisicalStaticSecretPush{ObjectMeta: metav1.ObjectMeta{Name: "c-deleting", Namespace: namespace}})
		deleting.Finalizers = []string{"test/keep"}
		env.update(deleting)
		Expect(env.client.Delete(env.ctx, deleting)).To(Succeed())

		Expect(env.handler.DeleteManagedSecrets(env.ctx, env.get(cleaning))).To(Succeed())

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
	})

	It("deletes a key two resources recorded when both are being deleted", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		first := env.mustSync(env.createNamedPush("first", []string{"source"}, []v1beta1.PushTarget{target("dev", "/")}))
		second := env.createNamedPush("second", []string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		second.Status.ManagedSecrets = first.Status.ManagedSecrets
		Expect(env.client.Status().Update(env.ctx, second)).To(Succeed())
		// The finalizer keeps the second resource around while it is being deleted.
		second = env.get(second)
		second.Finalizers = []string{"test/keep"}
		env.update(second)
		Expect(env.client.Delete(env.ctx, second)).To(Succeed())

		Expect(env.handler.DeleteManagedSecrets(env.ctx, env.get(first))).To(Succeed())

		Expect(env.values("dev", "/")).To(BeEmpty())
	})

	It("overwrites and takes over existing keys with updatePolicy Replace", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"EXISTING": "pushed"}))
		existingID := env.infisical.CreateSecret(projectID, "dev", "/", "EXISTING", "manual")
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})

		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"EXISTING": "pushed"}))
		Expect(push.Status.ManagedSecrets).To(Equal(map[string]string{existingID: "EXISTING"}))
	})

	It("deletes keys removed from the sources", func() {
		source := sourceSecret("source", map[string]string{"KEEP": "value", "REMOVE": "value"})
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		env.mustSync(push)

		delete(source.Data, "REMOVE")
		env.update(source)
		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEEP": "value"}))
		Expect(push.Status.ManagedSecrets).To(Equal(env.ids("dev", "/")))
	})

	It("deletes the keys of a source removed from the spec, unless another source still has them", func() {
		env := newTestEnv(
			sourceSecret("first", map[string]string{"SHARED": "first", "ONLY_FIRST": "value"}),
			sourceSecret("second", map[string]string{"SHARED": "second", "ONLY_SECOND": "value"}),
		)
		push := env.createPush([]string{"first", "second"}, []v1beta1.PushTarget{target("dev", "/")})
		env.mustSync(push)

		push = env.get(push)
		push.Spec.Sources.Secrets = push.Spec.Sources.Secrets[1:]
		env.update(push)
		env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"SHARED": "second", "ONLY_SECOND": "value"}))
	})

	It("keeps keys removed from the sources in Infisical with deletionPolicy None", func() {
		source := sourceSecret("source", map[string]string{"KEEP": "value", "REMOVE": "value"})
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")}, func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.DeletionPolicy = v1beta1.PushDeletionPolicyNone
		})
		env.mustSync(push)
		removedID := env.infisical.Secrets(projectID, "dev", "/")["REMOVE"].ID

		delete(source.Data, "REMOVE")
		env.update(source)
		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(HaveKey("REMOVE"))
		Expect(push.Status.ManagedSecrets).NotTo(HaveKey(removedID))
		Expect(env.infisical.CountRequests(deleteReq)).To(Equal(0))
	})

	It("deletes everything pushed to a removed target", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/"), target("prod", "/app")})
		env.mustSync(push)

		push = env.get(push)
		push.Spec.Targets = push.Spec.Targets[:1]
		env.update(push)
		push = env.mustSync(push)

		Expect(env.values("prod", "/app")).To(BeEmpty())
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
		Expect(push.Status.ManagedSecrets).To(Equal(env.ids("dev", "/")))
	})

	It("recreates a pushed secret that was deleted in Infisical", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		env.mustSync(push)
		oldID := env.infisical.Secrets(projectID, "dev", "/")["KEY"].ID
		env.infisical.DeleteSecret(oldID)

		push = env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
		Expect(push.Status.ManagedSecrets).NotTo(HaveKey(oldID))
		Expect(push.Status.ManagedSecrets).To(Equal(env.ids("dev", "/")))
	})

	It("resolves project slugs", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		env.infisical.AddProjectSlug("my-project", projectID)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{{ProjectSlug: "my-project", EnvironmentSlug: "dev", SecretPath: "/"}})

		env.mustSync(push)

		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
	})

	It("rejects two targets that point to the same folder", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		env.infisical.AddProjectSlug("my-project", projectID)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{
			target("dev", "/"),
			{ProjectSlug: "my-project", EnvironmentSlug: "dev", SecretPath: "/"},
		})

		_, err := env.sync(push)

		Expect(err).To(MatchError(ContainSubstring("targets[1] (project my-project, dev:/): points to the same project, environment and path as targets[0]")))
	})

	It("fails without pushing anything when a source Secret is missing", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source", "missing"}, []v1beta1.PushTarget{target("dev", "/")})

		push, err := env.sync(push)

		Expect(err).To(MatchError("source Secret default/missing not found"))
		Expect(env.infisical.CountRequests(upsertReq)).To(Equal(0))
		Expect(reconcileStatus(push).Message).To(Equal("Reconciliation failed: source Secret default/missing not found"))
	})

	It("keeps what it pushed when a source Secret disappears", func() {
		source := sourceSecret("source", map[string]string{"KEY": "value"})
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		managed := env.mustSync(push).Status.ManagedSecrets

		Expect(env.client.Delete(env.ctx, source)).To(Succeed())
		push, err := env.sync(push)

		Expect(err).To(HaveOccurred())
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
		Expect(push.Status.ManagedSecrets).To(Equal(managed))
	})

	It("rejects source values that are not valid UTF-8", func() {
		source := sourceSecret("source", map[string]string{"TEXT": "value"})
		source.Data["BINARY"] = []byte{0xff, 0xfe, 0x00}
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})

		_, err := env.sync(push)

		Expect(err).To(MatchError(ContainSubstring("source Secret default/source has values that are not valid UTF-8 in keys [BINARY]")))
		Expect(env.infisical.CountRequests(upsertReq)).To(Equal(0))
	})

	It("reports a target that requires approval without blocking the others", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		env.infisical.RequireApproval(projectID, "prod", "/")
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/"), target("prod", "/")})

		push, err := env.sync(push)

		Expect(err).To(MatchError(ContainSubstring("changes to prod:/ require approval")))
		Expect(err).To(MatchError(ContainSubstring("enable Bypass Approval for Machine Identities in the Change Policy")))
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
		Expect(push.Status.ManagedSecrets).To(Equal(env.ids("dev", "/")))
	})

	It("doesn't delete anything while a target fails, and records what it pushed", func() {
		source := sourceSecret("source", map[string]string{"KEEP": "value", "REMOVE": "value"})
		env := newTestEnv(source)
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/"), target("prod", "/")})
		previouslyManaged := env.mustSync(push).Status.ManagedSecrets

		delete(source.Data, "REMOVE")
		source.Data["ADDED"] = []byte("value")
		env.update(source)
		push = env.get(push)
		push.Spec.Targets[1].ProjectId = ""
		push.Spec.Targets[1].ProjectSlug = "unknown-project"
		env.update(push)
		push, err := env.sync(push)

		Expect(err).To(MatchError(ContainSubstring("targets[1]")))
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEEP": "value", "REMOVE": "value", "ADDED": "value"}))
		Expect(push.Status.ManagedSecrets).To(Equal(mergeMaps(previouslyManaged, env.ids("dev", "/"))))
	})

	It("stops calling Infisical when the context ends and still records the outcome", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		managed := env.mustSync(push).Status.ManagedSecrets
		DeferCleanup(env.infisical.Stall())

		ctx, cancel := context.WithTimeout(env.ctx, 200*time.Millisecond)
		defer cancel()
		_, err := env.handler.SyncSecrets(ctx, env.get(push))

		Expect(err).To(MatchError(context.DeadlineExceeded))
		push = env.get(push)
		Expect(push.Status.ManagedSecrets).To(Equal(managed))
		Expect(push.Status.Error).To(BeTrue())
		Expect(reconcileStatus(push).Message).To(ContainSubstring("context deadline exceeded"))
	})

	DescribeTable("rejects invalid specs",
		func(mutate func(*v1beta1.InfisicalStaticSecretPushSpec), message string) {
			env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
			push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")}, mutate)

			push, err := env.sync(push)

			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(push.Status.Error).To(BeTrue())
			Expect(env.infisical.Requests()).To(BeEmpty())
		},
		Entry("both projectId and projectSlug", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.Targets[0].ProjectSlug = "slug"
		}, "you declared both targets[0].projectId and targets[0].projectSlug"),
		Entry("neither projectId nor projectSlug", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.Targets[0].ProjectId = ""
		}, "either targets[0].projectId or targets[0].projectSlug must be set"),
		Entry("a relative secretPath", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.Targets[0].SecretPath = "app"
		}, "targets[0].secretPath must start with /"),
		Entry("duplicate targets", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.Targets = append(spec.Targets, target("dev", "/"))
		}, "targets[1] duplicates another target"),
		Entry("duplicate sources", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.Sources.Secrets = append(spec.Sources.Secrets, spec.Sources.Secrets[0])
		}, `duplicate source secret "default/source"`),
		Entry("an invalid refreshInterval", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.RefreshInterval = "2s"
		}, `invalid refreshInterval "2s"`),
		Entry("a zero refreshInterval in minutes", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.RefreshInterval = "0m"
		}, `invalid refreshInterval "0m": must be at least 5s`),
		Entry("a negative refreshInterval", func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.RefreshInterval = "-5m"
		}, `invalid refreshInterval "-5m": must be at least 5s`),
	)
})

var _ = Describe("DeleteManagedSecrets", func() {
	It("deletes every pushed secret, including secrets at targets removed earlier", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/"), target("prod", "/")}, func(spec *v1beta1.InfisicalStaticSecretPushSpec) {
			spec.SyncOptions.DeletionPolicy = v1beta1.PushDeletionPolicyNone
		})
		env.mustSync(push)
		// With deletionPolicy None, removing the target leaves its secrets in Infisical
		// and stops tracking them, so record them again to simulate a removed target
		// whose cleanup is still pending.
		push = env.get(push)
		push.Status.ManagedSecrets = mergeMaps(env.ids("dev", "/"), env.ids("prod", "/"))
		Expect(env.client.Status().Update(env.ctx, push)).To(Succeed())
		push = env.get(push)
		push.Spec.Targets = push.Spec.Targets[:1]
		env.update(push)

		Expect(env.handler.DeleteManagedSecrets(env.ctx, env.get(push))).To(Succeed())

		Expect(env.values("dev", "/")).To(BeEmpty())
		Expect(env.values("prod", "/")).To(BeEmpty())
		Expect(env.get(push).Status.ManagedSecrets).To(BeEmpty())
	})

	It("keeps the secrets it couldn't delete recorded", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/"), target("prod", "/")})
		managed := env.mustSync(push).Status.ManagedSecrets
		env.infisical.RequireApproval(projectID, "prod", "/")

		err := env.handler.DeleteManagedSecrets(env.ctx, env.get(push))

		Expect(err).To(MatchError(ContainSubstring("require approval")))
		push = env.get(push)
		Expect(push.Status.ManagedSecrets).To(Equal(env.ids("prod", "/")))
		Expect(managed).To(HaveLen(2))
		Expect(reconcileStatus(push).Message).To(ContainSubstring("unable to delete pushed secrets"))
	})

	It("does nothing when nothing was pushed", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})

		Expect(env.handler.DeleteManagedSecrets(env.ctx, push)).To(Succeed())
		Expect(env.infisical.Requests()).To(BeEmpty())
	})

	It("shows on the status that it is deleting while the deletion runs", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.mustSync(env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")}))
		release := env.infisical.Stall()
		DeferCleanup(release)

		done := make(chan error, 1)
		go func() { done <- env.handler.DeleteManagedSecrets(env.ctx, push) }()

		Eventually(func() string { return reconcileStatus(env.get(push)).Reason }).Should(Equal("Deleting"))
		Expect(reconcileStatus(env.get(push)).Message).To(Equal("Deleting the pushed secrets from Infisical"))
		release()
		Eventually(done).Should(Receive(BeNil()))
		Expect(env.values("dev", "/")).To(BeEmpty())
	})

	It("stops calling Infisical when the context ends and still records the outcome", func() {
		env := newTestEnv(sourceSecret("source", map[string]string{"KEY": "value"}))
		push := env.createPush([]string{"source"}, []v1beta1.PushTarget{target("dev", "/")})
		managed := env.mustSync(push).Status.ManagedSecrets
		DeferCleanup(env.infisical.Stall())

		ctx, cancel := context.WithTimeout(env.ctx, 200*time.Millisecond)
		defer cancel()
		err := env.handler.DeleteManagedSecrets(ctx, env.get(push))

		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(env.values("dev", "/")).To(Equal(map[string]string{"KEY": "value"}))
		push = env.get(push)
		Expect(push.Status.ManagedSecrets).To(Equal(managed))
		Expect(push.Status.Error).To(BeTrue())
		Expect(reconcileStatus(push).Message).To(ContainSubstring("unable to delete pushed secrets"))
	})
})

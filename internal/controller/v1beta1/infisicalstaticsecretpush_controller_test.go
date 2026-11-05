package v1beta1_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	controllerv1beta1 "github.com/Infisical/infisical/k8-operator/internal/controller/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalsession"
	"github.com/Infisical/infisical/k8-operator/internal/testutil/fakeinfisical"
)

var _ = Describe("InfisicalStaticSecretPush Controller", func() {
	const (
		projectID = "project-1"
		pushName  = "static-secret-push"
	)

	var (
		ctx        context.Context
		infisical  *fakeinfisical.Server
		reconciler *controllerv1beta1.InfisicalStaticSecretPushReconciler
		pushKey    types.NamespacedName
		authRef    secretsv1beta1.NamespacedName
	)

	markReady := func(obj client.Object, conditions *[]metav1.Condition) {
		*conditions = []metav1.Condition{{
			Type:               "secrets.infisical.com/IsReady",
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			LastTransitionTime: metav1.Now(),
		}}
		Expect(k8sClient.Status().Update(ctx, obj)).To(Succeed())
	}

	createAndCleanup := func(obj client.Object) {
		Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		DeferCleanup(func() {
			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, obj))
		})
	}

	BeforeEach(func() {
		ctx = context.Background()
		infisical = fakeinfisical.New([]string{projectID}, []string{"dev"})
		DeferCleanup(infisical.Close)

		suffix := fmt.Sprintf("%d", time.Now().UnixNano())
		pushKey = types.NamespacedName{Name: pushName + "-" + suffix, Namespace: "default"}

		connection := &secretsv1beta1.InfisicalConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "push-connection-" + suffix, Namespace: "default"},
			Spec:       secretsv1beta1.InfisicalConnectionSpec{Address: infisical.URL},
		}
		createAndCleanup(connection)
		markReady(connection, &connection.Status.Conditions)

		infisicalAuth := &secretsv1beta1.InfisicalAuth{
			ObjectMeta: metav1.ObjectMeta{Name: "push-auth-" + suffix, Namespace: "default"},
			Spec: secretsv1beta1.InfisicalAuthSpec{
				InfisicalConnectionRef: secretsv1beta1.NamespacedName{Name: connection.Name, Namespace: "default"},
				Method:                 secretsv1beta1.UniversalAuth,
				Universal: &secretsv1beta1.UniversalAuthConfig{
					ClientIdRef:     secretsv1beta1.SecretReference{Name: "unused", Namespace: "default", Key: "clientId"},
					ClientSecretRef: secretsv1beta1.SecretReference{Name: "unused", Namespace: "default", Key: "clientSecret"},
				},
			},
		}
		createAndCleanup(infisicalAuth)
		markReady(infisicalAuth, &infisicalAuth.Status.Conditions)
		authRef = secretsv1beta1.NamespacedName{Name: infisicalAuth.Name, Namespace: "default"}

		createAndCleanup(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "push-source-" + suffix, Namespace: "default"},
			StringData: map[string]string{"KEY": "value"},
		})

		authCache, err := cache.NewAuthCache()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(authCache.Cleanup)

		reconciler = &controllerv1beta1.InfisicalStaticSecretPushReconciler{
			Client:     k8sClient,
			BaseLogger: log.Log,
			Scheme:     k8sClient.Scheme(),
			AuthResolver: auth.NewAuthStrategyResolverForTesting(k8sClient, authCache, map[secretsv1beta1.InfisicalAuthMethod]auth.InfisicalAuthStrategy{
				secretsv1beta1.UniversalAuth: successStrategy(),
			}),
		}
	})

	createPush := func(syncOptions secretsv1beta1.PushSyncOptions) {
		suffix := pushKey.Name[len(pushName)+1:]
		push := &secretsv1beta1.InfisicalStaticSecretPush{
			ObjectMeta: metav1.ObjectMeta{Name: pushKey.Name, Namespace: pushKey.Namespace},
			Spec: secretsv1beta1.InfisicalStaticSecretPushSpec{
				InfisicalAuthRef: authRef,
				SyncOptions:      syncOptions,
				Sources: secretsv1beta1.PushSources{Secrets: []secretsv1beta1.NamespacedName{
					{Name: "push-source-" + suffix, Namespace: "default"},
				}},
				Targets: []secretsv1beta1.PushTarget{{ProjectId: projectID, EnvironmentSlug: "dev", SecretPath: "/app"}},
			},
		}
		Expect(k8sClient.Create(ctx, push)).To(Succeed())
		DeferCleanup(func() {
			latest := &secretsv1beta1.InfisicalStaticSecretPush{}
			if err := k8sClient.Get(ctx, pushKey, latest); err == nil {
				controllerutil.RemoveFinalizer(latest, constants.INFISICAL_STATIC_SECRET_PUSH_FINALIZER_NAME)
				_ = k8sClient.Update(ctx, latest)
				_ = client.IgnoreNotFound(k8sClient.Delete(ctx, latest))
			}
		})
	}

	getPush := func() *secretsv1beta1.InfisicalStaticSecretPush {
		push := &secretsv1beta1.InfisicalStaticSecretPush{}
		Expect(k8sClient.Get(ctx, pushKey, push)).To(Succeed())
		return push
	}

	reconcilePush := func() reconcile.Result {
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: pushKey})
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	hasFinalizer := func() bool {
		return controllerutil.ContainsFinalizer(getPush(), constants.INFISICAL_STATIC_SECRET_PUSH_FINALIZER_NAME)
	}

	pushedValues := func() map[string]string {
		values := map[string]string{}
		for key, secret := range infisical.Secrets(projectID, "dev", "/app") {
			values[key] = secret.Value
		}
		return values
	}

	It("applies the documented defaults", func() {
		createPush(secretsv1beta1.PushSyncOptions{})

		syncOptions := getPush().Spec.SyncOptions
		Expect(syncOptions.UpdatePolicy).To(Equal(secretsv1beta1.PushUpdatePolicyReplace))
		Expect(syncOptions.DeletionPolicy).To(Equal(secretsv1beta1.PushDeletionPolicyDelete))
		Expect(syncOptions.CreateMissingFolders).To(HaveValue(BeTrue()))
		Expect(syncOptions.RefreshInterval).To(BeEmpty())
	})

	It("adds the finalizer, pushes the secrets and requeues after the refresh interval", func() {
		createPush(secretsv1beta1.PushSyncOptions{RefreshInterval: "1m"})

		result := reconcilePush()

		Expect(result.RequeueAfter).To(Equal(time.Minute))
		Expect(hasFinalizer()).To(BeTrue())
		Expect(pushedValues()).To(Equal(map[string]string{"KEY": "value"}))
		Expect(getPush().Status.ManagedSecrets).To(HaveLen(1))
	})

	It("doesn't requeue without a refresh interval", func() {
		createPush(secretsv1beta1.PushSyncOptions{})

		Expect(reconcilePush()).To(Equal(reconcile.Result{}))
	})

	It("only keeps a finalizer while deletionPolicy is Delete", func() {
		createPush(secretsv1beta1.PushSyncOptions{DeletionPolicy: secretsv1beta1.PushDeletionPolicyNone})
		reconcilePush()
		Expect(hasFinalizer()).To(BeFalse())

		push := getPush()
		push.Spec.SyncOptions.DeletionPolicy = secretsv1beta1.PushDeletionPolicyDelete
		Expect(k8sClient.Update(ctx, push)).To(Succeed())
		reconcilePush()
		Expect(hasFinalizer()).To(BeTrue())

		push = getPush()
		push.Spec.SyncOptions.DeletionPolicy = secretsv1beta1.PushDeletionPolicyNone
		Expect(k8sClient.Update(ctx, push)).To(Succeed())
		reconcilePush()
		Expect(hasFinalizer()).To(BeFalse())
	})

	It("deletes the pushed secrets and removes the finalizer when it is deleted", func() {
		createPush(secretsv1beta1.PushSyncOptions{})
		reconcilePush()
		Expect(pushedValues()).NotTo(BeEmpty())

		Expect(k8sClient.Delete(ctx, getPush())).To(Succeed())
		reconcilePush()

		Expect(pushedValues()).To(BeEmpty())
		err := k8sClient.Get(ctx, pushKey, &secretsv1beta1.InfisicalStaticSecretPush{})
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
	})

	It("keeps the finalizer and retries while the cleanup fails within the grace period", func() {
		createPush(secretsv1beta1.PushSyncOptions{})
		reconcilePush()
		infisical.RequireApproval(projectID, "dev", "/app")

		Expect(k8sClient.Delete(ctx, getPush())).To(Succeed())
		result := reconcilePush()

		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(result.RequeueAfter).To(BeNumerically("<=", 15*time.Second))
		Expect(hasFinalizer()).To(BeTrue())
		Expect(getPush().Status.Error).To(BeTrue())
	})

	It("removes the finalizer after the grace period even when the cleanup fails", func() {
		createPush(secretsv1beta1.PushSyncOptions{})
		reconcilePush()
		infisical.RequireApproval(projectID, "dev", "/app")
		reconciler.CleanupGracePeriod = time.Nanosecond

		Expect(k8sClient.Delete(ctx, getPush())).To(Succeed())
		reconcilePush()

		Expect(pushedValues()).To(Equal(map[string]string{"KEY": "value"}))
		err := k8sClient.Get(ctx, pushKey, &secretsv1beta1.InfisicalStaticSecretPush{})
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
	})

	It("leaves the pushed secrets in Infisical when deleted with deletionPolicy None", func() {
		createPush(secretsv1beta1.PushSyncOptions{DeletionPolicy: secretsv1beta1.PushDeletionPolicyNone})
		reconcilePush()

		Expect(k8sClient.Delete(ctx, getPush())).To(Succeed())

		err := k8sClient.Get(ctx, pushKey, &secretsv1beta1.InfisicalStaticSecretPush{})
		Expect(k8sErrors.IsNotFound(err)).To(BeTrue())
		Expect(pushedValues()).To(Equal(map[string]string{"KEY": "value"}))
	})
})

var _ = Describe("InfisicalStaticSecretPush watches", func() {
	push := &secretsv1beta1.InfisicalStaticSecretPush{
		ObjectMeta: metav1.ObjectMeta{Name: "push", Namespace: "apps"},
		Spec: secretsv1beta1.InfisicalStaticSecretPushSpec{
			Sources: secretsv1beta1.PushSources{Secrets: []secretsv1beta1.NamespacedName{
				{Name: "db", Namespace: "apps"},
				{Name: "shared", Namespace: "platform"},
			}},
		},
	}

	It("indexes resources by their source Secrets", func() {
		Expect(controllerv1beta1.IndexStaticSecretPushBySourceSecret(push)).To(Equal([]string{"apps/db", "platform/shared"}))
	})

	It("indexes resources without an auth ref by the default InfisicalAuth", func() {
		defaultAuth := infisicalsession.ResolveAuthRef(secretsv1beta1.NamespacedName{})
		Expect(controllerv1beta1.IndexStaticSecretPushByAuth(push)).To(Equal([]string{defaultAuth.Namespace + "/" + defaultAuth.Name}))

		withRef := push.DeepCopy()
		withRef.Spec.InfisicalAuthRef = secretsv1beta1.NamespacedName{Name: "auth", Namespace: "apps"}
		Expect(controllerv1beta1.IndexStaticSecretPushByAuth(withRef)).To(Equal([]string{"apps/auth"}))
	})

	It("only reacts to Secret updates that change the data", func() {
		predicate := controllerv1beta1.SecretDataChangedPredicate()
		original := &corev1.Secret{Data: map[string][]byte{"KEY": []byte("value")}}

		relabeled := original.DeepCopy()
		relabeled.Labels = map[string]string{"team": "a"}
		Expect(predicate.Update(event.UpdateEvent{ObjectOld: original, ObjectNew: relabeled})).To(BeFalse())

		changed := original.DeepCopy()
		changed.Data["KEY"] = []byte("changed")
		Expect(predicate.Update(event.UpdateEvent{ObjectOld: original, ObjectNew: changed})).To(BeTrue())

		Expect(predicate.Create(event.CreateEvent{Object: original})).To(BeTrue())
		Expect(predicate.Delete(event.DeleteEvent{Object: original})).To(BeTrue())
	})

	It("reacts when deletion starts", func() {
		predicate := controllerv1beta1.DeletionStartedPredicate()
		deleting := push.DeepCopy()
		deleting.DeletionTimestamp = &metav1.Time{Time: time.Now()}

		Expect(predicate.Update(event.UpdateEvent{ObjectOld: push, ObjectNew: deleting})).To(BeTrue())
		Expect(predicate.Update(event.UpdateEvent{ObjectOld: push, ObjectNew: push.DeepCopy()})).To(BeFalse())
	})
})

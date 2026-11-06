package v1beta1

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/cache"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalsession"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalstaticsecretpush"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

const (
	// staticSecretPushSourceSecretIndex indexes InfisicalStaticSecretPushes by the
	// namespace/name of each source Secret.
	staticSecretPushSourceSecretIndex = ".spec.sources.secrets"
	// staticSecretPushAuthIndex indexes InfisicalStaticSecretPushes by the namespace/name
	// of the InfisicalAuth they use, after defaulting.
	staticSecretPushAuthIndex = ".spec.infisicalAuthRef"

	// staticSecretPushCleanupGracePeriod bounds how long the deletion of an
	// InfisicalStaticSecretPush waits for its pushed secrets to be deleted from
	// Infisical. After it, the finalizer is removed and the remaining secrets are left
	// in Infisical, so a broken InfisicalAuth or an unreachable Infisical can't block
	// a namespace deletion or an uninstall indefinitely.
	staticSecretPushCleanupGracePeriod = 5 * time.Minute
	staticSecretPushCleanupRetryDelay  = 15 * time.Second
)

type InfisicalStaticSecretPushReconciler struct {
	client.Client
	BaseLogger        logr.Logger
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	AuthResolver      *auth.AuthStrategyResolver
	ProjectIDCache    *cache.ResourceCache[string]
	// MaxConcurrentReconciles is the number of InfisicalStaticSecretPushes reconciled in parallel.
	// The workqueue guarantees a single resource is never reconciled by two workers at once.
	MaxConcurrentReconciles int
	// CleanupGracePeriod overrides the default grace period for deleting the pushed secrets when set.
	CleanupGracePeriod time.Duration
}

func (r *InfisicalStaticSecretPushReconciler) cleanupGracePeriod() time.Duration {
	if r.CleanupGracePeriod > 0 {
		return r.CleanupGracePeriod
	}
	return staticSecretPushCleanupGracePeriod
}

func (r *InfisicalStaticSecretPushReconciler) GetLogger(req ctrl.Request) logr.Logger {
	return r.BaseLogger.WithValues("infisicalstaticsecretpush", req.NamespacedName)
}

// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalstaticsecretpushes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalstaticsecretpushes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalstaticsecretpushes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="authentication.k8s.io",resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create

func (r *InfisicalStaticSecretPushReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.GetLogger(req)

	var push secretsv1beta1.InfisicalStaticSecretPush
	if err := r.Get(ctx, req.NamespacedName, &push); err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return ctrl.Result{}, model.NewNamespaceScopedError(err, "InfisicalStaticSecretPush")
		}

		if k8sErrors.IsNotFound(err) {
			logger.Info("Static Secret Push CRD not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "Unable to fetch Infisical Static Secret Push CRD from cluster")
		return ctrl.Result{}, fmt.Errorf("unable to fetch Infisical Static Secret Push CRD from cluster: %w", err)
	}

	pushHandler := infisicalstaticsecretpush.NewInfisicalStaticSecretPushHandler(r.Client, r.IsNamespaceScoped, r.AuthResolver, r.ProjectIDCache, logger)

	if !push.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, logger, pushHandler, &push)
	}

	// The finalizer is only needed to delete the pushed secrets, so it follows the deletion policy.
	wantsFinalizer := push.Spec.SyncOptions.GetDeletionPolicy() == secretsv1beta1.PushDeletionPolicyDelete
	if err := r.setFinalizer(ctx, &push, wantsFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	// Writes to Infisical are logged as they happen, so the summary only reports how
	// many keys the source Secrets hold.
	sourceKeys, err := pushHandler.SyncSecrets(ctx, &push)
	if err != nil {
		if result, ok := requeueOnRateLimit(logger, err); ok {
			return result, nil
		}

		return ctrl.Result{}, err
	}

	refreshInterval, err := infisicalstaticsecretpush.RefreshInterval(&push)
	if err != nil {
		return ctrl.Result{}, err
	}

	if refreshInterval == 0 {
		logger.Info(fmt.Sprintf("Reconciliation successful with %d secrets from the source Secrets, syncing again when a source Secret or this resource changes", sourceKeys))
		return ctrl.Result{}, nil
	}

	logger.Info(fmt.Sprintf("Reconciliation successful with %d secrets from the source Secrets, requeueing after %v", sourceKeys, refreshInterval))
	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

// reconcileDelete deletes the pushed secrets, retrying until the cleanup grace period has
// passed, and then removes the finalizer. Requests to Infisical are cancelled when the
// grace period ends.
func (r *InfisicalStaticSecretPushReconciler) reconcileDelete(
	ctx context.Context,
	logger logr.Logger,
	pushHandler *infisicalstaticsecretpush.InfisicalStaticSecretPushHandler,
	push *secretsv1beta1.InfisicalStaticSecretPush,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(push, constants.INFISICAL_STATIC_SECRET_PUSH_FINALIZER_NAME) {
		return ctrl.Result{}, nil
	}

	if push.Spec.SyncOptions.GetDeletionPolicy() == secretsv1beta1.PushDeletionPolicyDelete && len(push.Status.ManagedSecrets) > 0 {
		gracePeriod := r.cleanupGracePeriod()
		deadline := push.DeletionTimestamp.Add(gracePeriod)

		var err error
		if time.Now().Before(deadline) {
			cleanupCtx, cancel := context.WithDeadline(ctx, deadline)
			err = pushHandler.DeleteManagedSecrets(cleanupCtx, push)
			cancel()
		} else {
			err = fmt.Errorf("the cleanup grace period of %v has passed", gracePeriod)
		}

		if err != nil {
			if remaining := time.Until(deadline); remaining > 0 {
				retryAfter := staticSecretPushCleanupRetryDelay
				if delay, rateLimited := rateLimitDelay(err); rateLimited {
					retryAfter = withRateLimitJitter(delay)
				}
				retryAfter = min(retryAfter, remaining)

				logger.Error(err, fmt.Sprintf("Unable to delete pushed secrets from Infisical, retrying after %v", retryAfter.Round(100*time.Millisecond)))
				return ctrl.Result{RequeueAfter: retryAfter}, nil
			}

			logger.Error(err, fmt.Sprintf("Unable to delete pushed secrets from Infisical within %v, removing the finalizer and leaving them in Infisical", gracePeriod),
				"secretIds", slices.Sorted(maps.Keys(push.Status.ManagedSecrets)))
		}
	}

	if err := r.setFinalizer(ctx, push, false); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("Removed finalizer from Static Secret Push CRD")
	return ctrl.Result{}, nil
}

// setFinalizer adds or removes the finalizer. The patch carries the resource version,
// so it fails on a conflict instead of overwriting finalizers set by someone else.
func (r *InfisicalStaticSecretPushReconciler) setFinalizer(ctx context.Context, push *secretsv1beta1.InfisicalStaticSecretPush, present bool) error {
	if controllerutil.ContainsFinalizer(push, constants.INFISICAL_STATIC_SECRET_PUSH_FINALIZER_NAME) == present {
		return nil
	}

	original := push.DeepCopy()
	if present {
		controllerutil.AddFinalizer(push, constants.INFISICAL_STATIC_SECRET_PUSH_FINALIZER_NAME)
	} else {
		controllerutil.RemoveFinalizer(push, constants.INFISICAL_STATIC_SECRET_PUSH_FINALIZER_NAME)
	}

	if err := r.Patch(ctx, push, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("unable to update finalizers: %w", err)
	}
	return nil
}

func (r *InfisicalStaticSecretPushReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	indexer := mgr.GetFieldIndexer()

	if err := indexer.IndexField(ctx, &secretsv1beta1.InfisicalStaticSecretPush{}, staticSecretPushSourceSecretIndex, indexStaticSecretPushBySourceSecret); err != nil {
		return fmt.Errorf("unable to index InfisicalStaticSecretPush by source Secret: %w", err)
	}

	if err := indexer.IndexField(ctx, &secretsv1beta1.InfisicalStaticSecretPush{}, staticSecretPushAuthIndex, indexStaticSecretPushByAuth); err != nil {
		return fmt.Errorf("unable to index InfisicalStaticSecretPush by InfisicalAuth: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		// Deleting a resource that has a finalizer changes its generation, so the
		// generation predicate also admits the start of a deletion.
		For(&secretsv1beta1.InfisicalStaticSecretPush{}, builder.WithPredicates(predicate.Or[client.Object](
			predicate.GenerationChangedPredicate{},
			predicate.AnnotationChangedPredicate{},
		))).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.FindStaticSecretPushesReferencingSecret),
			builder.WithPredicates(secretDataChangedPredicate()),
		).
		Watches(&secretsv1beta1.InfisicalAuth{}, handler.EnqueueRequestsFromMapFunc(r.FindStaticSecretPushesReferencingAuth)).
		Complete(r)
}

// FindStaticSecretPushesReferencingSecret returns the InfisicalStaticSecretPushes that use the Secret as a source.
func (r *InfisicalStaticSecretPushReconciler) FindStaticSecretPushesReferencingSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.findStaticSecretPushes(ctx, staticSecretPushSourceSecretIndex, client.ObjectKeyFromObject(obj).String())
}

// FindStaticSecretPushesReferencingAuth returns the InfisicalStaticSecretPushes that use the
// InfisicalAuth, including the ones without an explicit auth ref when it is the default InfisicalAuth.
func (r *InfisicalStaticSecretPushReconciler) FindStaticSecretPushesReferencingAuth(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.findStaticSecretPushes(ctx, staticSecretPushAuthIndex, client.ObjectKeyFromObject(obj).String())
}

func (r *InfisicalStaticSecretPushReconciler) findStaticSecretPushes(ctx context.Context, index, key string) []reconcile.Request {
	var pushList secretsv1beta1.InfisicalStaticSecretPushList
	if err := r.List(ctx, &pushList, client.MatchingFields{index: key}, client.UnsafeDisableDeepCopy); err != nil {
		r.BaseLogger.Error(err, "Unable to list InfisicalStaticSecretPushes", "index", index, "key", key)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(pushList.Items))
	for i := range pushList.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&pushList.Items[i])})
	}
	return requests
}

func indexStaticSecretPushBySourceSecret(obj client.Object) []string {
	push, ok := obj.(*secretsv1beta1.InfisicalStaticSecretPush)
	if !ok {
		return nil
	}

	keys := make([]string, 0, len(push.Spec.Sources.Secrets))
	for _, source := range push.Spec.Sources.Secrets {
		keys = append(keys, types.NamespacedName{Namespace: source.Namespace, Name: source.Name}.String())
	}
	return keys
}

func indexStaticSecretPushByAuth(obj client.Object) []string {
	push, ok := obj.(*secretsv1beta1.InfisicalStaticSecretPush)
	if !ok {
		return nil
	}

	ref := infisicalsession.ResolveAuthRef(push.Spec.InfisicalAuthRef)
	return []string{types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}.String()}
}

// secretDataChangedPredicate admits Secret events that can change what is pushed.
func secretDataChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldSecret, oldOk := e.ObjectOld.(*corev1.Secret)
			newSecret, newOk := e.ObjectNew.(*corev1.Secret)
			if !oldOk || !newOk {
				return true
			}
			// This runs for every Secret update in the cluster, so the values are compared
			// directly instead of through reflection.
			return !maps.EqualFunc(oldSecret.Data, newSecret.Data, bytes.Equal)
		},
	}
}

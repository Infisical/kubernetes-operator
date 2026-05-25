package v1beta1

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/go-logr/logr"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/api"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalstaticsecret"
	"github.com/Infisical/infisical/k8-operator/internal/util"
	"github.com/Infisical/infisical/k8-operator/internal/util/sse"
)

type sseRegistryStore struct {
	mu      sync.Mutex
	entries map[string]map[string]*sse.ConnectionRegistry
}

func (s *sseRegistryStore) SubscribeForEvents(
	ctx context.Context,
	uid string,
	crd *secretsv1beta1.InfisicalStaticSecret,
	handler *infisicalstaticsecret.InfisicalAuthHandler,
	eventCh chan event.TypedGenericEvent[client.Object],
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	registries := s.entries[uid]
	updatedRegistries, err := handler.OpenInstantUpdatesStreams(ctx, crd, registries, eventCh)
	if err != nil {
		return err
	}
	s.entries[uid] = updatedRegistries
	return nil
}

func (s *sseRegistryStore) Cleanup(uid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if registries, ok := s.entries[uid]; ok {
		infisicalstaticsecret.CloseInstantUpdatesStreams(registries)
		delete(s.entries, uid)
	}
}

var sseRegistries = &sseRegistryStore{
	entries: make(map[string]map[string]*sse.ConnectionRegistry),
}

type InfisicalStaticSecretReconciler struct {
	client.Client
	BaseLogger        logr.Logger
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	AuthResolver      *auth.AuthStrategyResolver
	SourceCh          chan event.TypedGenericEvent[client.Object]
}

func (r *InfisicalStaticSecretReconciler) GetLogger(req ctrl.Request) logr.Logger {
	return r.BaseLogger.WithValues("infisicalstaticsecret", req.NamespacedName)
}

// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalstaticsecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalstaticsecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;update
// +kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=list;watch;get;update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=list;watch;get;update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list
// +kubebuilder:rbac:groups="authentication.k8s.io",resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create
func (r *InfisicalStaticSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.GetLogger(req)

	var staticSecretCRD secretsv1beta1.InfisicalStaticSecret

	err := r.Get(ctx, req.NamespacedName, &staticSecretCRD)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return ctrl.Result{}, model.NewNamespaceScopedError(err, "InfisicalStaticSecret")
		}

		if k8sErrors.IsNotFound(err) {
			logger.Info("Static Secret CRD not found")
			return ctrl.Result{
				RequeueAfter: 0,
			}, nil
		}

		logger.Error(err, "Unable to fetch Infisical Static Secret CRD from cluster")
		return ctrl.Result{}, fmt.Errorf("unable to fetch Infisical Static Secret CRD from cluster: %w", err)
	}

	crdUID := string(staticSecretCRD.UID)

	if !staticSecretCRD.DeletionTimestamp.IsZero() {
		sseRegistries.Cleanup(crdUID)
		return ctrl.Result{}, nil
	}

	instantUpdates := staticSecretCRD.Spec.SyncOptions != nil && staticSecretCRD.Spec.SyncOptions.InstantUpdates

	handler := infisicalstaticsecret.NewInfisicalStaticSecretHandler(r.Client, r.Scheme, r.IsNamespaceScoped, r.AuthResolver, logger)
	err = handler.SyncSecrets(ctx, &staticSecretCRD)
	if err != nil {
		var rateLimitErr *api.TooManyRequestsError
		if errors.As(err, &rateLimitErr) {
			retryAfter := rateLimitErr.RetryAfter
			jitter := time.Duration(1+rand.IntN(2)) * time.Second

			return ctrl.Result{
				RequeueAfter: time.Duration(retryAfter)*time.Second + jitter,
			}, nil
		}

		return ctrl.Result{}, err
	}

	if instantUpdates {
		if sseErr := sseRegistries.SubscribeForEvents(ctx, crdUID, &staticSecretCRD, handler, r.SourceCh); sseErr != nil {
			logger.Error(sseErr, "instant updates stream failed, falling back to periodic sync only")
		} else {
			logger.Info("Instant updates are enabled")
		}
	} else {
		sseRegistries.Cleanup(crdUID)
	}

	refreshInterval, err := time.ParseDuration(staticSecretCRD.Spec.SyncOptions.RefreshInterval)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("invalid refreshInterval %q: %w", staticSecretCRD.Spec.SyncOptions.RefreshInterval, err)
	}

	logger.Info(fmt.Sprintf("Reconciliation successful, requeueing after %v", refreshInterval))
	return ctrl.Result{
		RequeueAfter: refreshInterval,
	}, nil
}

func (r *InfisicalStaticSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.SourceCh = make(chan event.TypedGenericEvent[client.Object])

	return ctrl.NewControllerManagedBy(mgr).
		WatchesRawSource(
			source.Channel(r.SourceCh, &util.EnqueueDelayedEventHandler{Delay: time.Second * 10}),
		).
		For(&secretsv1beta1.InfisicalStaticSecret{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return true
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				if e.ObjectOld.GetGeneration() == e.ObjectNew.GetGeneration() {
					return false
				}
				sseRegistries.Cleanup(string(e.ObjectNew.GetUID()))
				return true
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				sseRegistries.Cleanup(string(e.Object.GetUID()))
				return true
			},
			GenericFunc: func(e event.GenericEvent) bool {
				return true
			},
		})).
		Watches(&secretsv1beta1.InfisicalAuth{}, handler.EnqueueRequestsFromMapFunc(r.FindStaticSecretsReferencingAuth)).
		Complete(r)
}

func (r *InfisicalStaticSecretReconciler) FindStaticSecretsReferencingAuth(ctx context.Context, obj client.Object) []reconcile.Request {
	authCRD, ok := obj.(*secretsv1beta1.InfisicalAuth)
	if !ok {
		return nil
	}

	var staticSecretList secretsv1beta1.InfisicalStaticSecretList
	if err := r.List(ctx, &staticSecretList); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, staticSecret := range staticSecretList.Items {
		ref := staticSecret.Spec.InfisicalAuthRef
		if ref.Name == authCRD.Name && ref.Namespace == authCRD.Namespace {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      staticSecret.Name,
					Namespace: staticSecret.Namespace,
				},
			})
		}
	}
	return requests
}

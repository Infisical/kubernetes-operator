package v1beta1

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalstaticsecret"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

type InfisicalStaticSecretReconciler struct {
	client.Client
	BaseLogger        logr.Logger
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	AuthResolver      *auth.AuthStrategyResolver
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

	var staticSecretCDR secretsv1beta1.InfisicalStaticSecret

	err := r.Get(ctx, req.NamespacedName, &staticSecretCDR)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return ctrl.Result{}, model.NewNamespaceScopedError(err, "InfisicalStaticSecret")
		}

		if errors.IsNotFound(err) {
			logger.Info("Static Secret CRD not found")
			return ctrl.Result{
				RequeueAfter: 0,
			}, nil
		}

		logger.Error(err, "Unable to fetch Infisical Static Secret CRD from cluster")
		return ctrl.Result{}, fmt.Errorf("unable to fetch Infisical Static Secret CRD from cluster: %w", err)
	}

	// If it's being deleted, we should not attempt to do anything
	// As this is a simple CRD, we don't need a finalizer to cleanup either.
	if !staticSecretCDR.DeletionTimestamp.IsZero() {
		// TODO: add finalizers
		return ctrl.Result{}, nil
	}

	handler := infisicalstaticsecret.NewInfisicalStaticSecretHandler(r.Client, r.Scheme, r.IsNamespaceScoped, r.AuthResolver, logger)
	err = handler.SyncSecrets(ctx, &staticSecretCDR)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *InfisicalStaticSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1beta1.InfisicalStaticSecret{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
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

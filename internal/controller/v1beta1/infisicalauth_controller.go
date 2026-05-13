package v1beta1

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/Infisical/infisical/k8-operator/internal/model"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalauth"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

type InfisicalAuthReconciler struct {
	client.Client
	BaseLogger        logr.Logger
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
	AuthResolver      *auth.AuthStrategyResolver
}

func (r *InfisicalAuthReconciler) GetLogger(req ctrl.Request) logr.Logger {
	return r.BaseLogger.WithValues("infisicalauth", req.NamespacedName)
}

// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalauths,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalauths/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list
//+kubebuilder:rbac:groups="authentication.k8s.io",resources=tokenreviews,verbs=create
//+kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create

func (r *InfisicalAuthReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.GetLogger(req)

	var authCRD secretsv1beta1.InfisicalAuth

	err := r.Get(ctx, req.NamespacedName, &authCRD)
	if err != nil {
		if util.IsNamespaceScopedError(err, r.IsNamespaceScoped) {
			return ctrl.Result{}, model.NewNamespaceScopedError(err, "InfisicalAuth")
		}

		if errors.IsNotFound(err) {
			logger.Info("Infisical Auth CRD not found")
			return ctrl.Result{
				RequeueAfter: 0,
			}, nil
		}

		logger.Error(err, "Unable to fetch Infisical Auth CRD from cluster")
		return ctrl.Result{}, fmt.Errorf("unable to fetch Infisical Auth CRD from cluster: %w", err)
	}

	// If it's being deleted, we should not attempt to do anything
	// As this is a simple CRD, we don't need a finalizer to cleanup either.
	if !authCRD.DeletionTimestamp.IsZero() {
		// If we are deleting the CRD, we must ensure cache is cleaned for this one.
		r.AuthResolver.DeleteCacheEntry(&authCRD)
		return ctrl.Result{}, nil
	}

	handler := infisicalauth.NewInfisicalAuthHandler(r.Client, r.Scheme, r.IsNamespaceScoped, r.AuthResolver)
	err = handler.ValidateAndAuthenticate(ctx, logger, &authCRD)
	handler.SetReconcileConditionStatus(ctx, logger, &authCRD, err)

	return ctrl.Result{}, err
}

func (r *InfisicalAuthReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1beta1.InfisicalAuth{}, builder.WithPredicates(&SpecChangedPredicate{
			AuthResolver: r.AuthResolver,
		})).
		// Watch for secret references that got updated
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.findAuthCRDsReferencingSecret)).
		// Watch for updates on the referenced infisicalConnection, including status updates
		Watches(&secretsv1beta1.InfisicalConnection{}, handler.EnqueueRequestsFromMapFunc(r.findAuthCRDsReferencingConnection)).
		Complete(r)
}

package v1beta1

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/services/infisicalconnection"
)

type InfisicalConnectionReconciler struct {
	client.Client
	BaseLogger        logr.Logger
	Scheme            *runtime.Scheme
	IsNamespaceScoped bool
}

func (r *InfisicalConnectionReconciler) GetLogger(req ctrl.Request) logr.Logger {
	return r.BaseLogger.WithValues("infisicalconnection", req.NamespacedName)
}

// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=secrets.infisical.com,resources=infisicalconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list
//+kubebuilder:rbac:groups="authentication.k8s.io",resources=tokenreviews,verbs=create
//+kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create

func (r *InfisicalConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.GetLogger(req)

	var infisicalConnectionCRD secretsv1beta1.InfisicalConnection

	err := r.Get(ctx, req.NamespacedName, &infisicalConnectionCRD)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Infisical Connection CRD not found")
			return ctrl.Result{
				RequeueAfter: 0,
			}, nil
		}

		logger.Error(err, "Unable to fetch Infisical Connection CRD from cluster")
		return ctrl.Result{}, fmt.Errorf("unable to fetch Infisical Connection CRD from cluster: %w", err)
	}

	// Add finalizer if it doesn't exist
	// TODO: we might not need this as this CRD doesn't create any external resources in k8s
	if !controllerutil.ContainsFinalizer(&infisicalConnectionCRD, constants.INFISICAL_CONNECTION_FINALIZER_NAME) {
		controllerutil.AddFinalizer(&infisicalConnectionCRD, constants.INFISICAL_CONNECTION_FINALIZER_NAME)
		if err := r.Update(ctx, &infisicalConnectionCRD); err != nil {
			return ctrl.Result{}, err
		}
		// Return early - the update will trigger a new reconcile with the fresh object. We can only update the CRD once or we'll see "the object has been modified; please apply your changes to the latest version and try again"
		return ctrl.Result{}, nil
	}

	// Check if it's being deleted
	if !infisicalConnectionCRD.DeletionTimestamp.IsZero() {
		logger.Info("Handling deletion of InfisicalDynamicSecret")
		if controllerutil.ContainsFinalizer(&infisicalConnectionCRD, constants.INFISICAL_CONNECTION_FINALIZER_NAME) {
			// We remove finalizers before running deletion logic to be completely safe from stuck resources
			infisicalConnectionCRD.ObjectMeta.Finalizers = []string{}
			if err := r.Update(ctx, &infisicalConnectionCRD); err != nil {
				logger.Error(err, fmt.Sprintf("Error removing finalizers from InfisicalDynamicSecret %s", infisicalConnectionCRD.Name))
				return ctrl.Result{}, err
			}

			// TODO: Delete handling
			// We can remove the finalizer if we don't have to handle anything here.
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	handler := infisicalconnection.NewInfisicalConnectionHandler(r.Client, r.Scheme, r.IsNamespaceScoped)

	if err := handler.TestConnection(ctx, infisicalConnectionCRD); err != nil {
		logger.Error(err, "Unable to test connection")

		handler.SetReconcileConditionStatus(ctx, logger, &infisicalConnectionCRD, err)

		// Kubernetes will retry this in exponential backoff
		// This will ensure if it's a transient issue, it will be retried and solved.
		return ctrl.Result{}, err
	}

	handler.SetReconcileConditionStatus(ctx, logger, &infisicalConnectionCRD, nil)

	return ctrl.Result{}, nil
}

func (r *InfisicalConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1beta1.InfisicalConnection{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

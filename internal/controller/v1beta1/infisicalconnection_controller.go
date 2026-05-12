package v1beta1

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	secretsv1beta1 "github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/model"
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=list;watch;get;update
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list
//+kubebuilder:rbac:groups="authentication.k8s.io",resources=tokenreviews,verbs=create
//+kubebuilder:rbac:groups="",resources=serviceaccounts/token,verbs=create

func (r *InfisicalConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.GetLogger(req)
	handler := infisicalconnection.NewInfisicalConnectionHandler(r.Client, r.Scheme, r.IsNamespaceScoped)

	connection, err := handler.GetInfisicalConnection(ctx, req.NamespacedName)
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			logger.Info("Infisical Connection CRD not found")
			return ctrl.Result{
				RequeueAfter: 0,
			}, nil
		}

		if connection == nil {
			// Some other k8s API error, we should retry
			return ctrl.Result{}, fmt.Errorf("unable to fetch connection: %w", err)
		}

		handler.SetReconcileConditionStatus(ctx, logger, connection, err)

		if errors.Is(err, model.ErrValidation) {
			// We should not retry as spec is invalid
			return ctrl.Result{}, nil
		}

		// Any other error should be retried by k8s
		return ctrl.Result{}, fmt.Errorf("unable to fetch a ready connection: %w", err)
	}

	// If it's being deleted, we should not attempt to do anything
	// As this is a simple CRD, we don't need a finalizer to cleanup either.
	if !connection.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if err := handler.TestConnection(ctx, connection); err != nil {
		logger.Error(err, "Unable to test connection")

		handler.SetReconcileConditionStatus(ctx, logger, connection, err)

		// Kubernetes will retry this in exponential backoff
		// This will ensure if it's a transient issue, it will be retried and solved.
		return ctrl.Result{}, err
	}

	handler.SetReconcileConditionStatus(ctx, logger, connection, nil)

	return ctrl.Result{
		// We keep reconciling every 5 minutes to ensure the connection is still healthy
		// This helps identifing: host downtime, TLS cert rotation issues, and any other
		// runtime issue that might happen.
		RequeueAfter: 5 * time.Minute,
	}, nil
}

func (r *InfisicalConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&secretsv1beta1.InfisicalConnection{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}

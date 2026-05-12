package infisicalauth

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/auth"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func NewInfisicalAuthHandler(
	client client.Client,
	scheme *runtime.Scheme,
	isNamespaceScoped bool,
	authResolver *auth.AuthStrategyResolver,
) *InfisicalAuthHandler {
	return &InfisicalAuthHandler{
		Client:            client,
		Scheme:            scheme,
		IsNamespaceScoped: isNamespaceScoped,
		authResolver:      authResolver,
	}
}

type InfisicalAuthHandler struct {
	client.Client
	Scheme            *runtime.Scheme
	Random            *rand.Rand
	IsNamespaceScoped bool
	authResolver      *auth.AuthStrategyResolver
}

func (h *InfisicalAuthHandler) getInfisicalConnection(ctx context.Context, connectionRef v1beta1.InfisicalConnectionRef) (*v1beta1.InfisicalConnection, error) {
	conn := v1beta1.InfisicalConnection{}

	err := h.Client.Get(ctx, types.NamespacedName{
		Name:      connectionRef.Name,
		Namespace: connectionRef.Namespace,
	}, &conn)
	if err != nil {
		return nil, fmt.Errorf("Unable to fetch Infisical Connection CRD from cluster: %w", err)
	}

	readyCond := meta.FindStatusCondition(conn.Status.Conditions, "secrets.infisical.com/IsReady")
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		return nil, fmt.Errorf("InfisicalConnection is not ready")
	}

	return &conn, nil
}

func (r *InfisicalAuthHandler) ValidateAndAuthenticate(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth) error {
	conn, err := r.getInfisicalConnection(ctx, infisicalAuth.Spec.InfisicalConnectionRef)
	if err != nil {
		return err
	}

	if err := r.authResolver.Validate(ctx, infisicalAuth); err != nil {
		return err
	}

	_, err = r.authResolver.Authenticate(ctx, conn, infisicalAuth)
	return err
}

func (r *InfisicalAuthHandler) SetReconcileConditionStatus(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth, errorToConditionOn error) {
	if infisicalAuth.Status.Conditions == nil {
		infisicalAuth.Status.Conditions = []metav1.Condition{}
	}

	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalAuth.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/IsReady",
			Status:             metav1.ConditionTrue,
			Reason:             "Ok",
			Message:            "InfisicalConnection is ready to be used.",
			ObservedGeneration: infisicalAuth.Generation,
		})

		meta.SetStatusCondition(&infisicalAuth.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/AuthMethod",
			Status:             metav1.ConditionTrue,
			Reason:             "Ok",
			Message:            string(infisicalAuth.Spec.Method),
			ObservedGeneration: infisicalAuth.Generation,
		})
	} else {
		meta.SetStatusCondition(&infisicalAuth.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/IsReady",
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            fmt.Sprintf("infisicalAuth is not ready to be used due to an error: %v", errorToConditionOn),
			ObservedGeneration: infisicalAuth.Generation,
		})

		meta.SetStatusCondition(&infisicalAuth.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/AuthMethod",
			Status:             metav1.ConditionFalse,
			Reason:             "Ok",
			Message:            string(infisicalAuth.Spec.Method),
			ObservedGeneration: infisicalAuth.Generation,
		})
	}

	err := r.Client.Status().Update(ctx, infisicalAuth)
	if err != nil {
		logger.Error(err, "Could not set condition for IsReady")
	}
}

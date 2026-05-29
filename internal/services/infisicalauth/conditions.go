package infisicalauth

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *InfisicalAuthReconciler) SetReconcileConditionStatus(ctx context.Context, logger logr.Logger, infisicalAuth *v1beta1.InfisicalAuth, errorToConditionOn error) {
	if infisicalAuth.Status.Conditions == nil {
		infisicalAuth.Status.Conditions = []metav1.Condition{}
	}

	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalAuth.Status.Conditions, metav1.Condition{
			Type:               "secrets.infisical.com/IsReady",
			Status:             metav1.ConditionTrue,
			Reason:             "Ok",
			Message:            "InfisicalAuth is ready to be used.",
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

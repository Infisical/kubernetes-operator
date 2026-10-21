package infisicalstaticsecret

import (
	"context"
	"fmt"
	"time"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionLastSuccessfulReconcileAt = "secrets.infisical.com/LastSuccessfulReconcileAt"
	ConditionLastReconcileStatus       = "secrets.infisical.com/LastReconcileStatus"
	ConditionLastReconcileAuthMethod   = "secrets.infisical.com/LastReconcileAuthMethod"
	ConditionAutoRedeployReady         = "secrets.infisical.com/LastReconcileAffectedDeployments"
)

func initConditions(infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	if infisicalStaticSecret.Status.Conditions == nil {
		infisicalStaticSecret.Status.Conditions = []metav1.Condition{}
	}
}

func setLastSuccessfulReconcileAtCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	initConditions(infisicalStaticSecret)
	meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
		Type:               ConditionLastSuccessfulReconcileAt,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            time.Now().UTC().Format(time.RFC3339),
		ObservedGeneration: infisicalStaticSecret.Generation,
	})
}

func setAuthMethodCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, authMethod string) {
	initConditions(infisicalStaticSecret)
	meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
		Type:               ConditionLastReconcileAuthMethod,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            authMethod,
		ObservedGeneration: infisicalStaticSecret.Generation,
	})
}

func setReconcileStatusCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, errorToConditionOn error) {
	initConditions(infisicalStaticSecret)
	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileStatus,
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			Message:            "Reconciliation successful",
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
	} else {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileStatus,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            fmt.Sprintf("Reconciliation failed: %v", errorToConditionOn),
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
	}
}

func setAutoRedeployReadyCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, numDeployments int) {
	initConditions(infisicalStaticSecret)
	meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
		Type:               ConditionAutoRedeployReady,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            fmt.Sprintf("%d deployments were redeployed due to secret changes", numDeployments),
		ObservedGeneration: infisicalStaticSecret.Generation,
	})
}

func (r *InfisicalStaticSecretReconciler) UpdateConditions(ctx context.Context, infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	err := r.Client.Status().Update(ctx, infisicalStaticSecret)
	if err != nil {
		r.logger.Error(err, "Failed to update status conditions")
	}
}

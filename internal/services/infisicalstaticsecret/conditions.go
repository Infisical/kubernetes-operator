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
	ConditionLastSuccessfulReconcile  = "secrets.infisical.com/LastSuccessfulReconcile"
	ConditionLastReconcileAuthMethod  = "secrets.infisical.com/LastReconcileAuthMethod"
	ConditionGetAccessTokenSuccessful = "secrets.infisical.com/LastReconcileGetAccessTokenSuccessful"
	ConditionLastReconcileSuccessful  = "secrets.infisical.com/LastReconcileSuccessful"
	ConditionAutoRedeployReady        = "secrets.infisical.com/LastReconcileAutoRedeployReady"
)

func initConditions(infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	if infisicalStaticSecret.Status.Conditions == nil {
		infisicalStaticSecret.Status.Conditions = []metav1.Condition{}
	}
}

func setLastSuccessfulReconcileCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	initConditions(infisicalStaticSecret)
	meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
		Type:               ConditionLastSuccessfulReconcile,
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

func setAccessTokenCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, errorToConditionOn error) {
	initConditions(infisicalStaticSecret)
	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionGetAccessTokenSuccessful,
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			Message:            "Retrieved access token successfully",
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
	} else {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionGetAccessTokenSuccessful,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            fmt.Sprintf("Failed to retrieve access token: %v", errorToConditionOn),
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
	}
}

func setReconcileSuccessfulCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, errorToConditionOn error) {
	initConditions(infisicalStaticSecret)
	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileSuccessful,
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			Message:            "Reconciliation successful",
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
	} else {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileSuccessful,
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
		Message:            fmt.Sprintf("Found %d deployments ready for auto redeploy on secret change", numDeployments),
		ObservedGeneration: infisicalStaticSecret.Generation,
	})
}

func (r *InfisicalStaticSecretReconciler) FlushConditions(ctx context.Context, infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	err := r.Client.Status().Update(ctx, infisicalStaticSecret)
	if err != nil {
		r.logger.Error(err, "Failed to update status conditions")
	}
}

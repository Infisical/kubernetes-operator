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

	// ConditionReady is the single source of truth for the health of the resource.
	// It follows the standard Kubernetes "Ready" convention so tools such as ArgoCD health checks,
	// kstatus and `kubectl wait --for=condition=Ready` can consume it directly.
	ConditionReady = "Ready"
)

const (
	ReasonReconcileSucceeded   = "ReconcileSucceeded"
	ReasonValidationFailed     = "ValidationFailed"
	ReasonAuthenticationFailed = "AuthenticationFailed"
	ReasonSecretsFetchFailed   = "SecretsFetchFailed"
	ReasonTargetSyncFailed     = "TargetSyncFailed"
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

// setReconcileStatusCondition records the outcome of a reconciliation on both the Ready condition and the
// legacy LastReconcileStatus condition, and stamps status.observedGeneration. A nil error marks the resource as ready.
func setReconcileStatusCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, reason string, errorToConditionOn error) {
	initConditions(infisicalStaticSecret)
	infisicalStaticSecret.Status.ObservedGeneration = infisicalStaticSecret.Generation

	if errorToConditionOn == nil {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             ReasonReconcileSucceeded,
			Message:            "Secrets are synced",
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileStatus,
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			Message:            "Reconciliation successful",
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
	} else {
		meta.SetStatusCondition(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
			Type:               ConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            errorToConditionOn.Error(),
			ObservedGeneration: infisicalStaticSecret.Generation,
		})
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

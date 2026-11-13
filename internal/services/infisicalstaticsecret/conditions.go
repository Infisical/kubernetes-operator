package infisicalstaticsecret

import (
	"context"
	"fmt"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/conditions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionLastSuccessfulReconcileAt = conditions.LastSuccessfulReconcileAt
	ConditionLastReconcileStatus       = conditions.LastReconcileStatus
	ConditionLastReconcileAuthMethod   = conditions.LastReconcileAuthMethod
	ConditionAutoRedeployReady         = "secrets.infisical.com/LastReconcileAffectedDeployments"
)

func setLastSuccessfulReconcileAtCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret) {
	conditions.SetLastSuccessfulReconcileAt(&infisicalStaticSecret.Status.Conditions, infisicalStaticSecret.Generation)
}

func setAuthMethodCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, authMethod string) {
	conditions.SetAuthMethod(&infisicalStaticSecret.Status.Conditions, infisicalStaticSecret.Generation, authMethod)
}

// setReconcileStatusCondition records the outcome of a reconciliation on status.error, the LastReconcileStatus
// condition and status.observedGeneration. A nil error marks the resource as healthy.
func setReconcileStatusCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, errorToConditionOn error) {
	infisicalStaticSecret.Status.ObservedGeneration = infisicalStaticSecret.Generation
	infisicalStaticSecret.Status.Error = errorToConditionOn != nil
	conditions.SetReconcileStatus(&infisicalStaticSecret.Status.Conditions, infisicalStaticSecret.Generation, errorToConditionOn)
}

func setAutoRedeployReadyCondition(infisicalStaticSecret *v1beta1.InfisicalStaticSecret, numDeployments int) {
	conditions.Set(&infisicalStaticSecret.Status.Conditions, metav1.Condition{
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

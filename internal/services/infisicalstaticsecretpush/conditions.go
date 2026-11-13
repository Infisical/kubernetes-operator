package infisicalstaticsecretpush

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/conditions"
)

const (
	ConditionLastSuccessfulReconcileAt = conditions.LastSuccessfulReconcileAt
	ConditionLastReconcileStatus       = conditions.LastReconcileStatus
	ConditionLastReconcileAuthMethod   = conditions.LastReconcileAuthMethod
)

func setLastSuccessfulReconcileAtCondition(push *v1beta1.InfisicalStaticSecretPush) {
	conditions.SetLastSuccessfulReconcileAt(&push.Status.Conditions, push.Generation)
}

func setAuthMethodCondition(push *v1beta1.InfisicalStaticSecretPush, authMethod string) {
	conditions.SetAuthMethod(&push.Status.Conditions, push.Generation, authMethod)
}

// setReconcileStatusCondition records the outcome of a reconciliation on status.error, the LastReconcileStatus
// condition and status.observedGeneration. A nil error marks the resource as healthy.
func setReconcileStatusCondition(push *v1beta1.InfisicalStaticSecretPush, errorToConditionOn error) {
	push.Status.ObservedGeneration = push.Generation
	push.Status.Error = errorToConditionOn != nil
	conditions.SetReconcileStatus(&push.Status.Conditions, push.Generation, errorToConditionOn)
}

// setDeletingCondition records on the LastReconcileStatus condition that the pushed
// secrets are being deleted from Infisical.
func setDeletingCondition(push *v1beta1.InfisicalStaticSecretPush) {
	conditions.Set(&push.Status.Conditions, metav1.Condition{
		Type:               ConditionLastReconcileStatus,
		Status:             metav1.ConditionFalse,
		Reason:             "Deleting",
		Message:            "Deleting the pushed secrets from Infisical",
		ObservedGeneration: push.Generation,
	})
}

// patchStatus persists the status changes made since original was copied. A merge patch
// doesn't depend on the resource version, so it can't fail on a conflict and drop the
// recorded managedSecrets, which the operator needs to update and delete what it pushed.
func (r *InfisicalStaticSecretPushReconciler) patchStatus(ctx context.Context, push, original *v1beta1.InfisicalStaticSecretPush) error {
	// A merge patch only carries the fields that changed, so status.error would never be
	// written on a resource whose first sync succeeds, and its Error column would stay
	// empty. Diffing against the opposite value always includes it.
	original = original.DeepCopy()
	original.Status.Error = !push.Status.Error

	if err := r.Client.Status().Patch(ctx, push, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("unable to update status: %w", err)
	}
	return nil
}

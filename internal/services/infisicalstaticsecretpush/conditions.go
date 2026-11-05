package infisicalstaticsecretpush

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
)

const (
	ConditionLastSuccessfulReconcileAt = "secrets.infisical.com/LastSuccessfulReconcileAt"
	ConditionLastReconcileStatus       = "secrets.infisical.com/LastReconcileStatus"
	ConditionLastReconcileAuthMethod   = "secrets.infisical.com/LastReconcileAuthMethod"
)

func initConditions(push *v1beta1.InfisicalStaticSecretPush) {
	if push.Status.Conditions == nil {
		push.Status.Conditions = []metav1.Condition{}
	}
}

func setLastSuccessfulReconcileAtCondition(push *v1beta1.InfisicalStaticSecretPush) {
	initConditions(push)
	meta.SetStatusCondition(&push.Status.Conditions, metav1.Condition{
		Type:               ConditionLastSuccessfulReconcileAt,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            time.Now().UTC().Format(time.RFC3339),
		ObservedGeneration: push.Generation,
	})
}

func setAuthMethodCondition(push *v1beta1.InfisicalStaticSecretPush, authMethod string) {
	initConditions(push)
	meta.SetStatusCondition(&push.Status.Conditions, metav1.Condition{
		Type:               ConditionLastReconcileAuthMethod,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            authMethod,
		ObservedGeneration: push.Generation,
	})
}

// setReconcileStatusCondition records the outcome of a reconciliation on status.error, the LastReconcileStatus
// condition and status.observedGeneration. A nil error marks the resource as healthy.
func setReconcileStatusCondition(push *v1beta1.InfisicalStaticSecretPush, errorToConditionOn error) {
	initConditions(push)
	push.Status.ObservedGeneration = push.Generation
	push.Status.Error = errorToConditionOn != nil

	if errorToConditionOn == nil {
		meta.SetStatusCondition(&push.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileStatus,
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			Message:            "Reconciliation successful",
			ObservedGeneration: push.Generation,
		})
	} else {
		meta.SetStatusCondition(&push.Status.Conditions, metav1.Condition{
			Type:               ConditionLastReconcileStatus,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            fmt.Sprintf("Reconciliation failed: %v", errorToConditionOn),
			ObservedGeneration: push.Generation,
		})
	}
}

// setDeletingCondition records on the LastReconcileStatus condition that the pushed
// secrets are being deleted from Infisical.
func setDeletingCondition(push *v1beta1.InfisicalStaticSecretPush) {
	initConditions(push)
	meta.SetStatusCondition(&push.Status.Conditions, metav1.Condition{
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

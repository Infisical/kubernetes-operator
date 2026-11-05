// Package conditions records reconciliation outcomes on the status conditions that the
// v1beta1 secret resources share.
package conditions

import (
	"fmt"
	"time"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	LastSuccessfulReconcileAt = "secrets.infisical.com/LastSuccessfulReconcileAt"
	LastReconcileStatus       = "secrets.infisical.com/LastReconcileStatus"
	LastReconcileAuthMethod   = "secrets.infisical.com/LastReconcileAuthMethod"
)

// MaxMessageLength is the longest message, in bytes, the API server accepts for a status
// condition. A longer one would make it reject the whole status update, including the
// other status fields written with it.
const MaxMessageLength = 32768

// Set adds condition to conditions, or replaces the condition of the same type. A
// message longer than MaxMessageLength is truncated.
func Set(conditions *[]metav1.Condition, condition metav1.Condition) {
	condition.Message = truncateMessage(condition.Message)
	meta.SetStatusCondition(conditions, condition)
}

// SetLastSuccessfulReconcileAt records the current time as the last successful reconciliation.
func SetLastSuccessfulReconcileAt(conditions *[]metav1.Condition, generation int64) {
	Set(conditions, metav1.Condition{
		Type:               LastSuccessfulReconcileAt,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            time.Now().UTC().Format(time.RFC3339),
		ObservedGeneration: generation,
	})
}

// SetAuthMethod records the authentication method the last reconciliation used.
func SetAuthMethod(conditions *[]metav1.Condition, generation int64, authMethod string) {
	Set(conditions, metav1.Condition{
		Type:               LastReconcileAuthMethod,
		Status:             metav1.ConditionTrue,
		Reason:             "OK",
		Message:            authMethod,
		ObservedGeneration: generation,
	})
}

// SetReconcileStatus records the outcome of a reconciliation on the LastReconcileStatus
// condition. A nil error marks the resource as healthy.
func SetReconcileStatus(conditions *[]metav1.Condition, generation int64, err error) {
	if err == nil {
		Set(conditions, metav1.Condition{
			Type:               LastReconcileStatus,
			Status:             metav1.ConditionTrue,
			Reason:             "OK",
			Message:            "Reconciliation successful",
			ObservedGeneration: generation,
		})
		return
	}

	Set(conditions, metav1.Condition{
		Type:               LastReconcileStatus,
		Status:             metav1.ConditionFalse,
		Reason:             "Error",
		Message:            fmt.Sprintf("Reconciliation failed: %v", err),
		ObservedGeneration: generation,
	})
}

// truncateMessage shortens a message to MaxMessageLength bytes, cutting at a character
// boundary so the message stays valid UTF-8.
func truncateMessage(message string) string {
	if len(message) <= MaxMessageLength {
		return message
	}
	const suffix = "... (truncated)"
	cut := MaxMessageLength - len(suffix)
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut] + suffix
}

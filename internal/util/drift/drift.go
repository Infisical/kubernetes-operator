// Package drift compares the live state of a synced Kubernetes object against
// the desired state derived from Infisical. Alongside the version annotation it
// recomputes the etag of the live data, so out-of-band edits are detected even
// when the editor leaves the annotation untouched.
package drift

import (
	"fmt"
	"maps"

	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/crypto"
	corev1 "k8s.io/api/core/v1"
)

// Reason explains why a live object was reported as drifted. ReasonManualEdit
// is the notable one: it means the object was changed outside the operator.
type Reason string

const (
	ReasonNone            Reason = ""
	ReasonManualEdit      Reason = "ManualEdit"
	ReasonSourceChanged   Reason = "SourceChanged"
	ReasonMetadataChanged Reason = "MetadataChanged"
)

// DesiredState is the state a synced Secret or ConfigMap should hold after a
// reconcile. Data is kept as bytes for both kinds; a ConfigMap's string values
// are converted before hashing so live and desired etags are comparable.
type DesiredState struct {
	Data        map[string][]byte
	Labels      map[string]string
	Annotations map[string]string
}

// Etag is the version written to the SECRET_VERSION_ANNOTATION for this state.
// It is the single definition of the formula: callers derive the annotation
// value from here rather than spelling it out themselves.
func (d DesiredState) Etag() string {
	return etag(d.Data)
}

func (d DesiredState) metadataChanged(liveLabels, liveAnnotations map[string]string) bool {
	return !maps.Equal(liveLabels, d.Labels) || !maps.Equal(liveAnnotations, d.Annotations)
}

func etag(data map[string][]byte) string {
	return crypto.ComputeEtag([]byte(fmt.Sprintf("%v", data)))
}

// SecretChanged reports whether a live Secret has drifted from the desired
// state, and why.
func SecretChanged(live *corev1.Secret, desired DesiredState) (bool, Reason) {
	annotated := live.Annotations[constants.SECRET_VERSION_ANNOTATION]
	return changed(annotated, etag(live.Data), desired.Etag(), desired.metadataChanged(live.Labels, live.Annotations))
}

// ConfigMapChanged reports whether a live ConfigMap has drifted from the
// desired state, and why.
func ConfigMapChanged(live *corev1.ConfigMap, desired DesiredState) (bool, Reason) {
	annotated := live.Annotations[constants.SECRET_VERSION_ANNOTATION]
	return changed(annotated, etag(asBytes(live.Data)), desired.Etag(), desired.metadataChanged(live.Labels, live.Annotations))
}

// changed ranks the three ways a target can diverge. The manual edit is checked
// first so that an out-of-band change is still reported as such when Infisical
// happens to have moved in the same reconcile.
func changed(annotated, liveEtag, desiredEtag string, metadataChanged bool) (bool, Reason) {
	// An empty annotation means this target has never been synced, so there is
	// no recorded version for the live data to have drifted from.
	if annotated != "" && liveEtag != annotated {
		return true, ReasonManualEdit
	}

	if annotated != desiredEtag {
		return true, ReasonSourceChanged
	}

	if metadataChanged {
		return true, ReasonMetadataChanged
	}

	return false, ReasonNone
}

// asBytes converts a ConfigMap's string data so it hashes identically to the
// byte-keyed desired data.
func asBytes(data map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(data))
	for key, value := range data {
		out[key] = []byte(value)
	}
	return out
}

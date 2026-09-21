package drift_test

import (
	"testing"

	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/Infisical/infisical/k8-operator/internal/util/drift"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var synced = map[string][]byte{"DB_HOST": []byte("localhost"), "DB_PORT": []byte("5432")}

// desiredFor builds the state the reconciler would pass for data, with the
// version annotation stamped the way SyncKubeSecret stamps it.
func desiredFor(data map[string][]byte) drift.DesiredState {
	d := drift.DesiredState{Data: data, Labels: map[string]string{}, Annotations: map[string]string{}}
	d.Annotations[constants.SECRET_VERSION_ANNOTATION] = d.Etag()
	return d
}

func annotations(etag string) map[string]string {
	return map[string]string{constants.SECRET_VERSION_ANNOTATION: etag}
}

func TestSecretChanged(t *testing.T) {
	inSync := desiredFor(synced)

	tests := []struct {
		name       string
		live       *corev1.Secret
		desired    drift.DesiredState
		wantChange bool
		wantReason drift.Reason
	}{
		{
			name:       "in sync",
			live:       &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())}, Data: synced},
			desired:    inSync,
			wantChange: false,
			wantReason: drift.ReasonNone,
		},
		{
			name: "data edited out-of-band with the annotation left behind",
			live: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())},
				Data:       map[string][]byte{"DB_HOST": []byte("tampered"), "DB_PORT": []byte("5432")},
			},
			desired:    inSync,
			wantChange: true,
			wantReason: drift.ReasonManualEdit,
		},
		{
			name:       "value changed in Infisical",
			live:       &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())}, Data: synced},
			desired:    desiredFor(map[string][]byte{"DB_HOST": []byte("new-host"), "DB_PORT": []byte("5432")}),
			wantChange: true,
			wantReason: drift.ReasonSourceChanged,
		},
		{
			name:       "labels differ only",
			live:       &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag()), Labels: map[string]string{"env": "stale"}}, Data: synced},
			desired:    inSync,
			wantChange: true,
			wantReason: drift.ReasonMetadataChanged,
		},
		{
			name:       "never synced, so an edit is not attributable",
			live:       &corev1.Secret{Data: map[string][]byte{"PRE_EXISTING": []byte("x")}},
			desired:    inSync,
			wantChange: true,
			wantReason: drift.ReasonSourceChanged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, reason := drift.SecretChanged(tt.live, tt.desired)
			if changed != tt.wantChange || reason != tt.wantReason {
				t.Errorf("got (%v, %q), want (%v, %q)", changed, reason, tt.wantChange, tt.wantReason)
			}
		})
	}
}

func TestConfigMapChanged(t *testing.T) {
	inSync := desiredFor(synced)
	liveData := map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432"}

	tests := []struct {
		name       string
		live       *corev1.ConfigMap
		desired    drift.DesiredState
		wantChange bool
		wantReason drift.Reason
	}{
		{
			// String data must hash identically to the byte-keyed desired data,
			// or every reconcile would look like drift.
			name:       "in sync",
			live:       &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())}, Data: liveData},
			desired:    inSync,
			wantChange: false,
			wantReason: drift.ReasonNone,
		},
		{
			name: "data edited out-of-band with the annotation left behind",
			live: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())},
				Data:       map[string]string{"DB_HOST": "tampered", "DB_PORT": "5432"},
			},
			desired:    inSync,
			wantChange: true,
			wantReason: drift.ReasonManualEdit,
		},
		{
			name: "key added out-of-band",
			live: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())},
				Data:       map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432", "INJECTED": "x"},
			},
			desired:    inSync,
			wantChange: true,
			wantReason: drift.ReasonManualEdit,
		},
		{
			name:       "value changed in Infisical",
			live:       &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Annotations: annotations(inSync.Etag())}, Data: liveData},
			desired:    desiredFor(map[string][]byte{"DB_HOST": []byte("new-host"), "DB_PORT": []byte("5432")}),
			wantChange: true,
			wantReason: drift.ReasonSourceChanged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed, reason := drift.ConfigMapChanged(tt.live, tt.desired)
			if changed != tt.wantChange || reason != tt.wantReason {
				t.Errorf("got (%v, %q), want (%v, %q)", changed, reason, tt.wantChange, tt.wantReason)
			}
		})
	}
}

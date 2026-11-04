package infisicalsecret

import (
	"context"
	"testing"

	"github.com/Infisical/infisical/k8-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func reconcilerWith(t *testing.T, objs ...client.Object) *InfisicalSecretReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return &InfisicalSecretReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()}
}

func TestAnyManagedResourceMissing(t *testing.T) {
	ctx := context.Background()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "app-secrets", Namespace: "default"}}
	configMap := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"}}
	secretRefs := []v1alpha1.ManagedKubeSecretConfig{{SecretName: "app-secrets", SecretNamespace: "default"}}
	configMapRefs := []v1alpha1.ManagedKubeConfigMapConfig{{ConfigMapName: "app-config", ConfigMapNamespace: "default"}}

	if reconcilerWith(t, secret, configMap).anyManagedResourceMissing(ctx, secretRefs, configMapRefs) {
		t.Error("reported a missing resource although both exist")
	}
	if !reconcilerWith(t, configMap).anyManagedResourceMissing(ctx, secretRefs, configMapRefs) {
		t.Error("did not notice the deleted managed secret")
	}
	if !reconcilerWith(t, secret).anyManagedResourceMissing(ctx, secretRefs, configMapRefs) {
		t.Error("did not notice the deleted managed configmap")
	}
	if reconcilerWith(t).anyManagedResourceMissing(ctx, nil, nil) {
		t.Error("reported a missing resource with nothing managed")
	}
}

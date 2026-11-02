package controllerhelpers

import (
	"context"
	"testing"

	"github.com/Infisical/infisical/k8-operator/internal/constants"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testAnnotationKey = DEPLOYMENT_SECRET_NAME_ANNOTATION_PREFIX + ".managed"

func managedSecret(version string) corev1.Secret {
	return corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:        "managed",
		Namespace:   "default",
		Annotations: map[string]string{constants.SECRET_VERSION_ANNOTATION: version},
	}}
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// The workload already carries the current version on its metadata, but another
// controller has replaced the pod template annotations. That must not trigger another restart.
func objectMetaInSync(version string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: "app", Namespace: "default", Annotations: map[string]string{testAnnotationKey: version}}
}

func TestReconcileWorkloadsSkipWhenOnlyThePodTemplateAnnotationIsMissing(t *testing.T) {
	ctx := context.Background()
	secret := managedSecret("v1")

	t.Run("deployment", func(t *testing.T) {
		d := &appsv1.Deployment{ObjectMeta: objectMetaInSync("v1")}
		c := newFakeClient(t, d)
		before := getDeployment(t, c)
		if err := ReconcileDeployment(ctx, c, logr.Discard(), before, secret); err != nil {
			t.Fatal(err)
		}
		after := getDeployment(t, c)
		if after.ResourceVersion != before.ResourceVersion || after.Spec.Template.Annotations[testAnnotationKey] != "" {
			t.Fatalf("deployment was updated again although the secret did not change: %v", after.Spec.Template.Annotations)
		}
	})

	t.Run("daemonset", func(t *testing.T) {
		d := &appsv1.DaemonSet{ObjectMeta: objectMetaInSync("v1")}
		c := newFakeClient(t, d)
		before := getDaemonSet(t, c)
		if err := ReconcileDaemonSet(ctx, c, logr.Discard(), before, secret); err != nil {
			t.Fatal(err)
		}
		after := getDaemonSet(t, c)
		if after.ResourceVersion != before.ResourceVersion || after.Spec.Template.Annotations[testAnnotationKey] != "" {
			t.Fatalf("daemonset was updated again although the secret did not change: %v", after.Spec.Template.Annotations)
		}
	})

	t.Run("statefulset", func(t *testing.T) {
		s := &appsv1.StatefulSet{ObjectMeta: objectMetaInSync("v1")}
		c := newFakeClient(t, s)
		before := getStatefulSet(t, c)
		if err := ReconcileStatefulSet(ctx, c, logr.Discard(), before, secret); err != nil {
			t.Fatal(err)
		}
		after := getStatefulSet(t, c)
		if after.ResourceVersion != before.ResourceVersion || after.Spec.Template.Annotations[testAnnotationKey] != "" {
			t.Fatalf("statefulset was updated again although the secret did not change: %v", after.Spec.Template.Annotations)
		}
	})
}

func TestReconcileWorkloadsRestartWhenTheSecretVersionChanges(t *testing.T) {
	ctx := context.Background()
	secret := managedSecret("v2")

	t.Run("deployment", func(t *testing.T) {
		c := newFakeClient(t, &appsv1.Deployment{ObjectMeta: objectMetaInSync("v1")})
		if err := ReconcileDeployment(ctx, c, logr.Discard(), getDeployment(t, c), secret); err != nil {
			t.Fatal(err)
		}
		after := getDeployment(t, c)
		if after.Annotations[testAnnotationKey] != "v2" || after.Spec.Template.Annotations[testAnnotationKey] != "v2" {
			t.Fatalf("want both annotations at v2, got %v and %v", after.Annotations, after.Spec.Template.Annotations)
		}
	})

	t.Run("daemonset", func(t *testing.T) {
		c := newFakeClient(t, &appsv1.DaemonSet{ObjectMeta: objectMetaInSync("v1")})
		if err := ReconcileDaemonSet(ctx, c, logr.Discard(), getDaemonSet(t, c), secret); err != nil {
			t.Fatal(err)
		}
		after := getDaemonSet(t, c)
		if after.Annotations[testAnnotationKey] != "v2" || after.Spec.Template.Annotations[testAnnotationKey] != "v2" {
			t.Fatalf("want both annotations at v2, got %v and %v", after.Annotations, after.Spec.Template.Annotations)
		}
	})

	t.Run("statefulset", func(t *testing.T) {
		c := newFakeClient(t, &appsv1.StatefulSet{ObjectMeta: objectMetaInSync("v1")})
		if err := ReconcileStatefulSet(ctx, c, logr.Discard(), getStatefulSet(t, c), secret); err != nil {
			t.Fatal(err)
		}
		after := getStatefulSet(t, c)
		if after.Annotations[testAnnotationKey] != "v2" || after.Spec.Template.Annotations[testAnnotationKey] != "v2" {
			t.Fatalf("want both annotations at v2, got %v and %v", after.Annotations, after.Spec.Template.Annotations)
		}
	})
}

func getDeployment(t *testing.T, c client.Client) appsv1.Deployment {
	t.Helper()
	var d appsv1.Deployment
	if err := c.Get(context.Background(), client.ObjectKey{Name: "app", Namespace: "default"}, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func getDaemonSet(t *testing.T, c client.Client) appsv1.DaemonSet {
	t.Helper()
	var d appsv1.DaemonSet
	if err := c.Get(context.Background(), client.ObjectKey{Name: "app", Namespace: "default"}, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func getStatefulSet(t *testing.T, c client.Client) appsv1.StatefulSet {
	t.Helper()
	var s appsv1.StatefulSet
	if err := c.Get(context.Background(), client.ObjectKey{Name: "app", Namespace: "default"}, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

package metrics_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/metrics"
)

// staticSecret builds a minimal InfisicalStaticSecret. An empty lastReconcile leaves the
// resource without a LastReconcileStatus condition, as it is before its first reconcile.
func staticSecret(namespace, name, refreshInterval string, instantUpdates bool, authRef v1beta1.NamespacedName, lastReconcile metav1.ConditionStatus) *v1beta1.InfisicalStaticSecret {
	secret := &v1beta1.InfisicalStaticSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: v1beta1.InfisicalStaticSecretSpec{
			InfisicalAuthRef: authRef,
			SyncOptions: &v1beta1.SyncOptions{
				RefreshInterval: refreshInterval,
				InstantUpdates:  instantUpdates,
			},
		},
	}
	if lastReconcile != "" {
		secret.Status.Conditions = []metav1.Condition{{
			Type:   v1beta1.ConditionLastReconcileStatus,
			Status: lastReconcile,
			Reason: "Test",
		}}
	}
	return secret
}

var _ = Describe("StaticSecretCollector", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(v1beta1.AddToScheme(scheme)).To(Succeed())
	})

	It("derives the metrics from the resources in the cache", func() {
		sharedAuth := v1beta1.NamespacedName{Namespace: "infisical", Name: "shared-auth"}
		localAuth := v1beta1.NamespacedName{Namespace: "payments", Name: "local-auth"}

		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			staticSecret("payments", "synced", "30s", true, sharedAuth, metav1.ConditionTrue),
			staticSecret("payments", "failed", "5m", false, sharedAuth, metav1.ConditionFalse),
			staticSecret("payments", "pending", "1h", false, localAuth, ""),
			staticSecret("billing", "other", "not-a-duration", false, sharedAuth, metav1.ConditionTrue),
		).Build()

		expected := `
# HELP infisical_static_secrets Number of InfisicalStaticSecret resources.
# TYPE infisical_static_secrets gauge
infisical_static_secrets{namespace="billing"} 1
infisical_static_secrets{namespace="payments"} 3
# HELP infisical_static_secrets_sync_failed Number of InfisicalStaticSecret resources whose last reconciliation failed.
# TYPE infisical_static_secrets_sync_failed gauge
infisical_static_secrets_sync_failed{namespace="billing"} 0
infisical_static_secrets_sync_failed{namespace="payments"} 1
# HELP infisical_static_secret_sync_status Result of the last reconciliation of an InfisicalStaticSecret: 1 for the current status, 0 for the others.
# TYPE infisical_static_secret_sync_status gauge
infisical_static_secret_sync_status{name="synced",namespace="payments",status="synced"} 1
infisical_static_secret_sync_status{name="synced",namespace="payments",status="failed"} 0
infisical_static_secret_sync_status{name="synced",namespace="payments",status="unknown"} 0
infisical_static_secret_sync_status{name="failed",namespace="payments",status="synced"} 0
infisical_static_secret_sync_status{name="failed",namespace="payments",status="failed"} 1
infisical_static_secret_sync_status{name="failed",namespace="payments",status="unknown"} 0
infisical_static_secret_sync_status{name="pending",namespace="payments",status="synced"} 0
infisical_static_secret_sync_status{name="pending",namespace="payments",status="failed"} 0
infisical_static_secret_sync_status{name="pending",namespace="payments",status="unknown"} 1
infisical_static_secret_sync_status{name="other",namespace="billing",status="synced"} 1
infisical_static_secret_sync_status{name="other",namespace="billing",status="failed"} 0
infisical_static_secret_sync_status{name="other",namespace="billing",status="unknown"} 0
# HELP infisical_static_secret_refresh_interval_seconds Configured refreshInterval of an InfisicalStaticSecret in seconds.
# TYPE infisical_static_secret_refresh_interval_seconds gauge
infisical_static_secret_refresh_interval_seconds{name="synced",namespace="payments"} 30
infisical_static_secret_refresh_interval_seconds{name="failed",namespace="payments"} 300
infisical_static_secret_refresh_interval_seconds{name="pending",namespace="payments"} 3600
# HELP infisical_static_secret_info Configuration of an InfisicalStaticSecret. The value is always 1.
# TYPE infisical_static_secret_info gauge
infisical_static_secret_info{auth_name="shared-auth",auth_namespace="infisical",instant_updates="true",name="synced",namespace="payments"} 1
infisical_static_secret_info{auth_name="shared-auth",auth_namespace="infisical",instant_updates="false",name="failed",namespace="payments"} 1
infisical_static_secret_info{auth_name="local-auth",auth_namespace="payments",instant_updates="false",name="pending",namespace="payments"} 1
infisical_static_secret_info{auth_name="shared-auth",auth_namespace="infisical",instant_updates="false",name="other",namespace="billing"} 1
`
		collector := metrics.NewStaticSecretCollector(reader)
		Expect(testutil.CollectAndCompare(collector, strings.NewReader(expected))).To(Succeed())
	})

	It("emits nothing when the resources cannot be listed", func() {
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return errors.New("cache not started")
			},
		}).Build()

		collector := metrics.NewStaticSecretCollector(reader)
		Expect(testutil.CollectAndCount(collector)).To(Equal(0))
	})
})

var _ = Describe("Workload redeploy counter", func() {
	It("accumulates per secret and is removed when the secret is forgotten", func() {
		scheme := runtime.NewScheme()
		Expect(v1beta1.AddToScheme(scheme)).To(Succeed())
		reader := fake.NewClientBuilder().WithScheme(scheme).Build()

		registry := prometheus.NewPedanticRegistry()
		Expect(metrics.Register(registry, reader)).To(Succeed())

		metrics.RecordStaticSecretWorkloadRedeploys("payments", "db-creds", 0)
		metrics.RecordStaticSecretWorkloadRedeploys("payments", "db-creds", 2)
		metrics.RecordStaticSecretWorkloadRedeploys("payments", "db-creds", 1)

		const name = "infisical_static_secret_workload_redeploys_total"
		expected := `
# HELP infisical_static_secret_workload_redeploys_total Number of workloads redeployed by an InfisicalStaticSecret because one of its targets changed.
# TYPE infisical_static_secret_workload_redeploys_total counter
infisical_static_secret_workload_redeploys_total{name="db-creds",namespace="payments"} 3
`
		Expect(testutil.GatherAndCompare(registry, strings.NewReader(expected), name)).To(Succeed())

		metrics.ForgetStaticSecret("payments", "db-creds")
		Expect(testutil.GatherAndCount(registry, name)).To(Equal(0))
	})
})

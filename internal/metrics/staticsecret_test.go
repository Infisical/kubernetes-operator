package metrics_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/metrics"
)

// samples returns the values of a metric keyed by its labels, e.g. "name=x,namespace=y".
func samples(gatherer prometheus.Gatherer, name string) map[string]float64 {
	families, err := gatherer.Gather()
	Expect(err).NotTo(HaveOccurred())

	values := map[string]float64{}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			var labels []string
			for _, pair := range metric.GetLabel() {
				labels = append(labels, pair.GetName()+"="+pair.GetValue())
			}
			values[strings.Join(labels, ",")] = metric.GetGauge().GetValue() + metric.GetCounter().GetValue()
		}
	}
	return values
}

// staticSecret builds a minimal InfisicalStaticSecret. An empty lastReconcile leaves the
// resource without a LastReconcileStatus condition, as it is before its first reconcile.
func staticSecret(namespace, name, refreshInterval string, instantUpdates bool, lastReconcile metav1.ConditionStatus) *v1beta1.InfisicalStaticSecret {
	secret := &v1beta1.InfisicalStaticSecret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: v1beta1.InfisicalStaticSecretSpec{
			SyncOptions: &v1beta1.SyncOptions{
				RefreshInterval: refreshInterval,
				InstantUpdates:  instantUpdates,
			},
		},
	}
	if lastReconcile != "" {
		secret.Status.Conditions = []metav1.Condition{{
			Type:   "secrets.infisical.com/LastReconcileStatus",
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
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			staticSecret("payments", "synced", "30s", true, metav1.ConditionTrue),
			staticSecret("payments", "failed", "5m", false, metav1.ConditionFalse),
			staticSecret("payments", "pending", "1h", false, ""),
			staticSecret("billing", "other", "not-a-duration", false, metav1.ConditionTrue),
		).Build()

		registry := prometheus.NewRegistry()
		Expect(registry.Register(metrics.NewStaticSecretCollector(reader))).To(Succeed())

		Expect(samples(registry, "infisical_static_secrets")).To(Equal(map[string]float64{
			"instant_updates=true,name=synced,namespace=payments,status=synced":    1,
			"instant_updates=false,name=failed,namespace=payments,status=failed":   1,
			"instant_updates=false,name=pending,namespace=payments,status=unknown": 1,
			"instant_updates=false,name=other,namespace=billing,status=synced":     1,
		}))
		// The secret with an invalid refreshInterval has no interval series.
		Expect(samples(registry, "infisical_static_secret_refresh_interval_seconds")).To(Equal(map[string]float64{
			"name=synced,namespace=payments":  30,
			"name=failed,namespace=payments":  300,
			"name=pending,namespace=payments": 3600,
		}))
	})

	It("emits nothing when the resources cannot be listed", func() {
		reader := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
			List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
				return errors.New("cache not started")
			},
		}).Build()

		registry := prometheus.NewRegistry()
		Expect(registry.Register(metrics.NewStaticSecretCollector(reader))).To(Succeed())

		families, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		Expect(families).To(BeEmpty())
	})
})

var _ = Describe("Per-secret sync metrics", func() {
	const redeploys = "infisical_static_secret_workload_redeploys_total"
	const autoReloadWorkloads = "infisical_static_secret_auto_reload_workloads"

	It("are recorded per secret and removed when the secret is forgotten", func() {
		// The counter accumulates, the gauge keeps the latest value.
		metrics.RecordStaticSecretWorkloadRedeploys("payments", "db-creds", 0)
		metrics.RecordStaticSecretWorkloadRedeploys("payments", "db-creds", 2)
		metrics.RecordStaticSecretWorkloadRedeploys("payments", "db-creds", 1)
		metrics.RecordStaticSecretAutoReloadWorkloads("payments", "db-creds", 3)
		metrics.RecordStaticSecretAutoReloadWorkloads("payments", "db-creds", 2)

		Expect(samples(ctrlmetrics.Registry, redeploys)).To(Equal(map[string]float64{"name=db-creds,namespace=payments": 3}))
		Expect(samples(ctrlmetrics.Registry, autoReloadWorkloads)).To(Equal(map[string]float64{"name=db-creds,namespace=payments": 2}))

		metrics.ForgetStaticSecret("payments", "db-creds")
		Expect(samples(ctrlmetrics.Registry, redeploys)).To(BeEmpty())
		Expect(samples(ctrlmetrics.Registry, autoReloadWorkloads)).To(BeEmpty())
	})
})

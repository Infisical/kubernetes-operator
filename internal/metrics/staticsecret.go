package metrics

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/Infisical/infisical/k8-operator/api/v1beta1"
	"github.com/Infisical/infisical/k8-operator/internal/util"
)

const (
	syncStatusSynced  = "synced"
	syncStatusFailed  = "failed"
	syncStatusUnknown = "unknown"

	// collectTimeout bounds a single scrape so a stalled cache cannot hang Prometheus.
	collectTimeout = 10 * time.Second
)

var syncStatuses = []string{syncStatusSynced, syncStatusFailed, syncStatusUnknown}

var (
	staticSecretsDesc = prometheus.NewDesc(
		"infisical_static_secrets",
		"Number of InfisicalStaticSecret resources.",
		[]string{"namespace"}, nil,
	)
	staticSecretsSyncFailedDesc = prometheus.NewDesc(
		"infisical_static_secrets_sync_failed",
		"Number of InfisicalStaticSecret resources whose last reconciliation failed.",
		[]string{"namespace"}, nil,
	)
	staticSecretSyncStatusDesc = prometheus.NewDesc(
		"infisical_static_secret_sync_status",
		"Result of the last reconciliation of an InfisicalStaticSecret: 1 for the current status, 0 for the others.",
		[]string{"namespace", "name", "status"}, nil,
	)
	staticSecretRefreshIntervalDesc = prometheus.NewDesc(
		"infisical_static_secret_refresh_interval_seconds",
		"Configured refreshInterval of an InfisicalStaticSecret in seconds.",
		[]string{"namespace", "name"}, nil,
	)
	staticSecretInfoDesc = prometheus.NewDesc(
		"infisical_static_secret_info",
		"Configuration of an InfisicalStaticSecret. The value is always 1.",
		[]string{"namespace", "name", "auth_namespace", "auth_name", "instant_updates"}, nil,
	)

	staticSecretWorkloadRedeploys = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "infisical_static_secret_workload_redeploys_total",
		Help: "Number of workloads redeployed by an InfisicalStaticSecret because one of its targets changed.",
	}, []string{"namespace", "name"})
)

// Register adds the InfisicalStaticSecret metrics to the registry served at /metrics.
// The reader should be backed by the manager's cache so a scrape never hits the API server.
func Register(registry prometheus.Registerer, reader client.Reader) error {
	if err := registry.Register(staticSecretWorkloadRedeploys); err != nil {
		return err
	}
	return registry.Register(NewStaticSecretCollector(reader))
}

// RecordStaticSecretWorkloadRedeploys adds the workloads redeployed during one sync to the
// secret's counter. Calling it with zero still creates the series, so rate queries work from
// the first sync on.
func RecordStaticSecretWorkloadRedeploys(namespace, name string, count int) {
	staticSecretWorkloadRedeploys.WithLabelValues(namespace, name).Add(float64(count))
}

// ForgetStaticSecret drops the series owned by a deleted InfisicalStaticSecret.
func ForgetStaticSecret(namespace, name string) {
	staticSecretWorkloadRedeploys.DeleteLabelValues(namespace, name)
}

// StaticSecretCollector derives metrics from the InfisicalStaticSecret resources in the
// cache on every scrape, so nothing has to be kept in step with creates, updates or deletes.
type StaticSecretCollector struct {
	reader client.Reader
}

func NewStaticSecretCollector(reader client.Reader) *StaticSecretCollector {
	return &StaticSecretCollector{reader: reader}
}

func (c *StaticSecretCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- staticSecretsDesc
	ch <- staticSecretsSyncFailedDesc
	ch <- staticSecretSyncStatusDesc
	ch <- staticSecretRefreshIntervalDesc
	ch <- staticSecretInfoDesc
}

func (c *StaticSecretCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()

	var list v1beta1.InfisicalStaticSecretList
	if err := c.reader.List(ctx, &list); err != nil {
		// Skip this scrape instead of failing it, so the rest of /metrics stays available.
		ctrl.Log.WithName("metrics").Error(err, "Unable to list InfisicalStaticSecrets for metrics collection")
		return
	}

	total := make(map[string]int)
	failed := make(map[string]int)

	for i := range list.Items {
		secret := &list.Items[i]
		total[secret.Namespace]++

		status := syncStatus(secret)
		if status == syncStatusFailed {
			failed[secret.Namespace]++
		}
		for _, candidate := range syncStatuses {
			value := 0.0
			if candidate == status {
				value = 1
			}
			ch <- prometheus.MustNewConstMetric(staticSecretSyncStatusDesc, prometheus.GaugeValue, value,
				secret.Namespace, secret.Name, candidate)
		}

		instantUpdates := false
		if secret.Spec.SyncOptions != nil {
			instantUpdates = secret.Spec.SyncOptions.InstantUpdates
			if interval, err := util.ConvertResyncIntervalToDuration(secret.Spec.SyncOptions.RefreshInterval, false); err == nil {
				ch <- prometheus.MustNewConstMetric(staticSecretRefreshIntervalDesc, prometheus.GaugeValue, interval.Seconds(),
					secret.Namespace, secret.Name)
			}
		}

		ch <- prometheus.MustNewConstMetric(staticSecretInfoDesc, prometheus.GaugeValue, 1,
			secret.Namespace, secret.Name,
			secret.Spec.InfisicalAuthRef.Namespace, secret.Spec.InfisicalAuthRef.Name,
			strconv.FormatBool(instantUpdates))
	}

	for namespace, count := range total {
		ch <- prometheus.MustNewConstMetric(staticSecretsDesc, prometheus.GaugeValue, float64(count), namespace)
		ch <- prometheus.MustNewConstMetric(staticSecretsSyncFailedDesc, prometheus.GaugeValue, float64(failed[namespace]), namespace)
	}
}

func syncStatus(secret *v1beta1.InfisicalStaticSecret) string {
	condition := meta.FindStatusCondition(secret.Status.Conditions, v1beta1.ConditionLastReconcileStatus)
	switch {
	case condition == nil:
		return syncStatusUnknown
	case condition.Status == metav1.ConditionTrue:
		return syncStatusSynced
	case condition.Status == metav1.ConditionFalse:
		return syncStatusFailed
	default:
		return syncStatusUnknown
	}
}

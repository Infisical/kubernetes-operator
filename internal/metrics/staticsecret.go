// Package metrics holds the operator's Prometheus collectors. They register
// onto controller-runtime's registry, so they are served by the manager's
// existing metrics endpoint.
package metrics

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

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

// staticSecretWorkloadRedeploys counts the workloads that auto-reload redeployed
// because a target of an InfisicalStaticSecret changed.
var staticSecretWorkloadRedeploys = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "infisical_static_secret_workload_redeploys_total",
		Help: "Number of workloads redeployed by an InfisicalStaticSecret because one of its targets changed.",
	},
	[]string{"namespace", "name"},
)

// staticSecretAutoReloadWorkloads holds how many workloads opted into auto-reload
// consume the targets of an InfisicalStaticSecret.
var staticSecretAutoReloadWorkloads = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "infisical_static_secret_auto_reload_workloads",
		Help: "Number of workloads opted into auto-reload that consume a target of the InfisicalStaticSecret.",
	},
	[]string{"namespace", "name"},
)

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
)

func init() {
	ctrlmetrics.Registry.MustRegister(staticSecretWorkloadRedeploys, staticSecretAutoReloadWorkloads)
}

// RecordStaticSecretWorkloadRedeploys adds the workloads redeployed during one sync to the
// secret's counter. Calling it with zero still creates the series, so rate queries work from
// the first sync on.
func RecordStaticSecretWorkloadRedeploys(namespace, name string, count int) {
	staticSecretWorkloadRedeploys.WithLabelValues(namespace, name).Add(float64(count))
}

// RecordStaticSecretAutoReloadWorkloads sets the number of workloads opted into auto-reload
// that consume the secret's targets.
func RecordStaticSecretAutoReloadWorkloads(namespace, name string, count int) {
	staticSecretAutoReloadWorkloads.WithLabelValues(namespace, name).Set(float64(count))
}

// ForgetStaticSecret drops the series owned by a deleted InfisicalStaticSecret, so the
// number of series stays bounded by the resources that exist.
func ForgetStaticSecret(namespace, name string) {
	staticSecretWorkloadRedeploys.DeleteLabelValues(namespace, name)
	staticSecretAutoReloadWorkloads.DeleteLabelValues(namespace, name)
}

// StaticSecretCollector derives metrics from the InfisicalStaticSecret resources in the
// cache on every scrape, so nothing has to be kept in step with creates, updates or deletes.
type StaticSecretCollector struct {
	reader client.Reader
}

// NewStaticSecretCollector returns a collector that lists InfisicalStaticSecrets through the
// reader, which should be the manager's cached client so a scrape never hits the API server.
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
		// Skip these metrics for this scrape instead of failing it, so the rest of /metrics stays available.
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
	condition := meta.FindStatusCondition(secret.Status.Conditions, "secrets.infisical.com/LastReconcileStatus")
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

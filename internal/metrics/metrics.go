// Package metrics holds the operator's Prometheus collectors. They register
// onto controller-runtime's registry, so they are served by the manager's
// existing metrics endpoint.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

// manualDriftTotal counts reconciles that found a managed target whose data no
// longer matches the version the operator last wrote, which means it was edited
// outside the operator.
var manualDriftTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "infisical_managed_target_manual_drift_total",
		Help: "Number of times a managed Secret or ConfigMap was found edited outside the operator and replaced with the value from Infisical.",
	},
	[]string{"kind", "namespace", "name"},
)

func init() {
	ctrlmetrics.Registry.MustRegister(manualDriftTotal)
}

// RecordManualDrift reports that a managed target was found edited out-of-band.
func RecordManualDrift(kind, namespace, name string) {
	manualDriftTotal.WithLabelValues(kind, namespace, name).Inc()
}

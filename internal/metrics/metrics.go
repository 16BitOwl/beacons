// Package metrics provides Prometheus instrumentation for beacons.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all Prometheus instruments for beacons.
type Metrics struct {
	SyncTotal   *prometheus.CounterVec
	SyncLatency *prometheus.HistogramVec
	DriftTotal  *prometheus.CounterVec
}

// New registers all metrics with reg and returns a Metrics instance.
func New(reg prometheus.Registerer) *Metrics {
	syncTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "beacons_sync_operations_total",
		Help: "Total upstream sync operations.",
	}, []string{"upstream", "operation", "result"})

	syncLatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "beacons_sync_duration_seconds",
		Help:    "Latency of upstream sync operations.",
		Buckets: []float64{.01, .05, .1, .5, 1, 5},
	}, []string{"upstream", "operation"})

	driftTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "beacons_drift_corrections_total",
		Help: "Drift corrections found by upstream verification (store believed synced, upstream disagreed).",
	}, []string{"upstream", "reason"})

	reg.MustRegister(syncTotal, syncLatency, driftTotal)

	return &Metrics{SyncTotal: syncTotal, SyncLatency: syncLatency, DriftTotal: driftTotal}
}

// RecordSync increments the sync counter and records latency for the given
// upstream, operation ("upsert", "delete", or "list"), and result ("success"
// or "failure").
func (m *Metrics) RecordSync(upstream, operation, result string, dur time.Duration) {
	m.SyncTotal.WithLabelValues(upstream, operation, result).Inc()
	m.SyncLatency.WithLabelValues(upstream, operation).Observe(dur.Seconds())
}

// RecordDrift increments the drift-correction counter for the given upstream
// and reason (reconcile.DriftMissing or reconcile.DriftChanged).
func (m *Metrics) RecordDrift(upstream, reason string) {
	m.DriftTotal.WithLabelValues(upstream, reason).Inc()
}

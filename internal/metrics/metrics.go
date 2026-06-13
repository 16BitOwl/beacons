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

	reg.MustRegister(syncTotal, syncLatency)

	return &Metrics{SyncTotal: syncTotal, SyncLatency: syncLatency}
}

// RecordSync increments the sync counter and records latency for the given
// upstream, operation ("upsert" or "delete"), and result ("success" or "failure").
func (m *Metrics) RecordSync(upstream, operation, result string, dur time.Duration) {
	m.SyncTotal.WithLabelValues(upstream, operation, result).Inc()
	m.SyncLatency.WithLabelValues(upstream, operation).Observe(dur.Seconds())
}

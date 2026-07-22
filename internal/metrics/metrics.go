// Package metrics provides Prometheus instrumentation for beacons.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds all Prometheus instruments for beacons.
type Metrics struct {
	SyncTotal          *prometheus.CounterVec
	SyncLatency        *prometheus.HistogramVec
	DriftTotal         *prometheus.CounterVec
	UpstreamAPICalls   *prometheus.CounterVec
	UpstreamAPILatency *prometheus.HistogramVec
	CircuitBreakerOpen *prometheus.GaugeVec
	BackoffGatedTotal  *prometheus.CounterVec
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

	upstreamAPICalls := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "beacons_upstream_api_calls_total",
		Help: "HTTP attempts made to upstream APIs, one per retry attempt.",
	}, []string{"upstream", "method", "status"})

	upstreamAPILatency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "beacons_upstream_api_latency_seconds",
		Help:    "Latency of individual HTTP attempts to upstream APIs.",
		Buckets: []float64{.01, .05, .1, .5, 1, 5},
	}, []string{"upstream", "method"})

	circuitBreakerOpen := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "beacons_circuit_breaker_open",
		Help: "1 if an upstream's circuit breaker has tripped (too many consecutive auth failures), else 0.",
	}, []string{"upstream"})

	backoffGatedTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "beacons_backoff_gated_total",
		Help: "Reconcile ops skipped because the record is still within its post-failure backoff window.",
	}, []string{"upstream"})

	reg.MustRegister(syncTotal, syncLatency, driftTotal, upstreamAPICalls, upstreamAPILatency, circuitBreakerOpen, backoffGatedTotal)

	return &Metrics{
		SyncTotal:          syncTotal,
		SyncLatency:        syncLatency,
		DriftTotal:         driftTotal,
		UpstreamAPICalls:   upstreamAPICalls,
		UpstreamAPILatency: upstreamAPILatency,
		CircuitBreakerOpen: circuitBreakerOpen,
		BackoffGatedTotal:  backoffGatedTotal,
	}
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

// RecordAPICall implements transport.MetricsRecorder.
func (m *Metrics) RecordAPICall(upstream, method string, status int, dur time.Duration) {
	m.UpstreamAPICalls.WithLabelValues(upstream, method, statusLabel(status)).Inc()
	m.UpstreamAPILatency.WithLabelValues(upstream, method).Observe(dur.Seconds())
}

// SetCircuitBreakerOpen implements transport.MetricsRecorder.
func (m *Metrics) SetCircuitBreakerOpen(upstream string, open bool) {
	v := 0.0
	if open {
		v = 1
	}
	m.CircuitBreakerOpen.WithLabelValues(upstream).Set(v)
}

func (m *Metrics) RecordBackoffGated(upstream string) {
	m.BackoffGatedTotal.WithLabelValues(upstream).Inc()
}

func statusLabel(status int) string {
	if status == 0 {
		return "error"
	}
	return strconv.Itoa(status)
}

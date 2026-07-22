package transport

import (
	"net/http"
	"time"
)

// MetricsRecorder receives transport-level instrumentation. Nil disables it.
type MetricsRecorder interface {
	// status is 0 if the round trip errored before a response arrived.
	RecordAPICall(upstream, method string, status int, dur time.Duration)
	SetCircuitBreakerOpen(upstream string, open bool)
}

// Metrics returns a Middleware recording per-attempt call count/latency,
// tagged by HTTP method (see docs/operations/http-metrics.md). Place inside
// Retry so each attempt is recorded separately. Nil recorder is a no-op.
func Metrics(name string, recorder MetricsRecorder) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		if recorder == nil {
			return next
		}
		return &metricsTransport{next: next, name: name, recorder: recorder}
	}
}

type metricsTransport struct {
	next     http.RoundTripper
	name     string
	recorder MetricsRecorder
}

func (t *metricsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	t.recorder.RecordAPICall(t.name, req.Method, status, time.Since(start))
	return resp, err
}

package transport

import (
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
)

const defaultMaxAuthFailures = 5

// ErrCircuitOpen is returned by a tripped circuit-breaker transport. Callers
// should use errors.Is to detect it.
var ErrCircuitOpen = newCircuitOpenError()

type circuitOpenError struct{}

func newCircuitOpenError() *circuitOpenError { return &circuitOpenError{} }
func (e *circuitOpenError) Error() string {
	return "transport: circuit open after repeated authentication failures – check credentials and restart"
}

// CircuitBreakerOptions configures the circuit-breaker middleware.
type CircuitBreakerOptions struct {
	// Name identifies the upstream in log messages. Optional but recommended.
	Name string
	// MaxAuthFailures is the number of consecutive HTTP 401 responses that open
	// the circuit. Zero uses the default (5).
	MaxAuthFailures int
}

// CircuitBreaker returns a Middleware that opens the circuit after too many
// consecutive authentication failures — HTTP 401 (Unauthorized) or 403
// (Forbidden) responses, or errors wrapping [ErrAuthFailed] (a session
// authenticator whose credentials were rejected) — preventing further requests
// from reaching the upstream.
//
// All of these indicate a credentials or permissions problem that will not
// self-heal without a configuration change and restart.
//
// Once open, every call returns ErrCircuitOpen without making a network
// request. The circuit stays open for the lifetime of the process; recovering
// requires fixing the credentials and restarting.
//
// The failure counter resets to zero on any non-401/403 response, so transient
// failures don't accumulate against the threshold.
func CircuitBreaker(opts CircuitBreakerOptions) Middleware {
	maxFails := int32(opts.MaxAuthFailures)
	if maxFails <= 0 {
		maxFails = defaultMaxAuthFailures
	}
	return func(next http.RoundTripper) http.RoundTripper {
		return &circuitBreakerTransport{
			next:     next,
			name:     opts.Name,
			maxFails: maxFails,
		}
	}
}

type circuitBreakerTransport struct {
	next         http.RoundTripper
	name         string
	maxFails     int32
	authFailures atomic.Int32
	disabled     atomic.Bool
}

func (t *circuitBreakerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.disabled.Load() {
		return nil, ErrCircuitOpen
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		if errors.Is(err, ErrAuthFailed) {
			t.recordAuthFailure("err", err)
		}
		return nil, err
	}

	if isAuthError(resp.StatusCode) {
		t.recordAuthFailure("status", resp.StatusCode)
		return resp, nil
	}

	t.authFailures.Store(0)
	return resp, nil
}

// recordAuthFailure increments the consecutive-failure counter and opens the
// circuit once the threshold is reached. cause is logged under causeKey
// ("status" for 401/403 responses, "err" for ErrAuthFailed errors).
func (t *circuitBreakerTransport) recordAuthFailure(causeKey string, cause any) {
	n := t.authFailures.Add(1)
	if n >= t.maxFails && !t.disabled.Swap(true) {
		slog.Error("upstream disabled: too many consecutive authentication failures; check credentials and restart",
			"upstream", t.name,
			causeKey, cause,
			"consecutive_auth_failures", n)
	}
}

// isAuthError reports whether a status code indicates a credentials or
// permissions problem that is unlikely to resolve without a config change.
func isAuthError(code int) bool {
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}

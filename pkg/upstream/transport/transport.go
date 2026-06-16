// Package transport provides composable http.RoundTripper middleware for
// upstream HTTP clients.
//
// Build a transport by chaining middleware with [Chain]:
//
//	t := transport.Chain(nil,
//	    transport.Retry(retryOpts),
//	    transport.Bearer(apiToken),
//	)
//	client := &http.Client{Transport: t}
//
// Middleware is applied in declaration order: the first entry is outermost
// (first to see the request, last to see the response). In the example above
// Retry wraps Bearer, so each retry attempt passes through Bearer and gets a
// fresh set of request headers.
package transport

import (
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// Middleware wraps an http.RoundTripper with additional behaviour.
type Middleware func(http.RoundTripper) http.RoundTripper

// Chain builds an http.RoundTripper by applying middlewares to base in order.
// The first middleware is outermost. Pass nil for base to use
// http.DefaultTransport.
func Chain(base http.RoundTripper, middlewares ...Middleware) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	for i := len(middlewares) - 1; i >= 0; i-- {
		base = middlewares[i](base)
	}
	return base
}

// ---------------------------------------------------------------------------
// Retry middleware
// ---------------------------------------------------------------------------

const (
	defaultMaxAttempts = 3
	defaultBaseDelay   = 500 * time.Millisecond
	defaultMaxDelay    = 30 * time.Second
)

// RetryOptions configures retry behaviour.
// Zero values fall back to sensible defaults.
type RetryOptions struct {
	// MaxAttempts is the total number of attempts including the first. Default: 3.
	MaxAttempts int
	// BaseDelay is the initial backoff duration. Default: 500ms.
	BaseDelay time.Duration
	// MaxDelay caps the computed backoff. Default: 30s.
	MaxDelay time.Duration
}

func (o RetryOptions) withDefaults() RetryOptions {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = defaultMaxAttempts
	}
	if o.BaseDelay <= 0 {
		o.BaseDelay = defaultBaseDelay
	}
	if o.MaxDelay <= 0 {
		o.MaxDelay = defaultMaxDelay
	}
	return o
}

// Retry returns a Middleware that retries transient failures with exponential
// backoff and ±25% jitter.
//
// Retried conditions: network errors, HTTP 429, 500, 502, 503, 504.
// On HTTP 429, the Retry-After response header is honoured if present.
//
// Requests with a non-resettable body (GetBody == nil) are not retried after
// the first attempt.
func Retry(opts RetryOptions) Middleware {
	opts = opts.withDefaults()
	return func(next http.RoundTripper) http.RoundTripper {
		return &retryTransport{next: next, opts: opts}
	}
}

type retryTransport struct {
	next http.RoundTripper
	opts RetryOptions
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var (
		resp *http.Response
		err  error
	)
	for attempt := range t.opts.MaxAttempts {
		if attempt > 0 {
			delay := calcDelay(attempt, resp, t.opts)
			if err != nil {
				slog.Debug("transport: retrying request after error",
					"attempt", attempt+1,
					"max_attempts", t.opts.MaxAttempts,
					"delay", delay,
					"err", err)
			} else {
				slog.Debug("transport: retrying request after retryable status",
					"attempt", attempt+1,
					"max_attempts", t.opts.MaxAttempts,
					"delay", delay,
					"status", resp.StatusCode)
			}
			drainAndClose(resp)
			resp = nil

			if req.Body != nil {
				if req.GetBody == nil {
					// Body cannot be replayed; return whatever the last attempt gave us.
					return resp, err
				}
				if req.Body, err = req.GetBody(); err != nil {
					return nil, err
				}
			}

			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(delay):
			}
		}

		resp, err = t.next.RoundTrip(req)
		if err != nil {
			continue
		}
		if !isRetryable(resp.StatusCode) {
			return resp, nil
		}
	}
	return resp, err
}

// ---------------------------------------------------------------------------
// Header middleware
// ---------------------------------------------------------------------------

// Header returns a Middleware that sets a static header on every request.
func Header(name, value string) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &headerTransport{next: next, name: name, value: value}
	}
}

type headerTransport struct {
	next  http.RoundTripper
	name  string
	value string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set(t.name, t.value)
	return t.next.RoundTrip(r)
}


// ---------------------------------------------------------------------------
// Bearer middleware
// ---------------------------------------------------------------------------

// Bearer returns a Middleware that injects a static Bearer token into every
// request's Authorization header.
func Bearer(token string) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &bearerTransport{next: next, token: token}
	}
}

type bearerTransport struct {
	next  http.RoundTripper
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.next.RoundTrip(r)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// isAuthError reports whether a status code indicates a credentials or
// permissions problem that is unlikely to resolve without a config change.
func isAuthError(code int) bool {
	return code == http.StatusUnauthorized || code == http.StatusForbidden
}

func isRetryable(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// calcDelay returns the backoff duration for a given attempt (1-based).
// It honours the Retry-After header on 429 responses when available.
func calcDelay(attempt int, resp *http.Response, opts RetryOptions) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	base := float64(opts.BaseDelay) * math.Pow(2, float64(attempt-1))
	jitter := base * (rand.Float64()*0.5 - 0.25)
	d := time.Duration(base + jitter)
	if d > opts.MaxDelay {
		return opts.MaxDelay
	}
	return d
}

// drainAndClose discards the remaining response body and closes it so the
// underlying connection can be reused. Safe to call with nil.
func drainAndClose(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// ---------------------------------------------------------------------------
// CircuitBreaker middleware
// ---------------------------------------------------------------------------

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
// consecutive HTTP 401 (Unauthorized) or 403 (Forbidden) responses, preventing
// further requests from reaching the upstream.
//
// Both status codes indicate a credentials or permissions problem that will not
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
		return nil, err
	}

	if isAuthError(resp.StatusCode) {
		n := t.authFailures.Add(1)
		if n >= t.maxFails && !t.disabled.Swap(true) {
			slog.Error("upstream disabled: too many consecutive authentication failures; check credentials and restart",
				"upstream", t.name,
				"status", resp.StatusCode,
				"consecutive_auth_failures", n)
		}
		return resp, nil
	}

	t.authFailures.Store(0)
	return resp, nil
}

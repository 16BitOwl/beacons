// Package transport provides composable http.RoundTripper middleware for
// upstream HTTP clients. Middleware is applied in declaration order: the first
// entry is outermost (first to see the request, last to see the response).
package transport

import (
	"io"
	"net/http"
	"time"
)

const defaultAttemptTimeout = 15 * time.Second

// ClientOptions configures NewClient.
type ClientOptions struct {
	// Name identifies the upstream in circuit-breaker log messages. Optional.
	Name string
	// Timeout bounds each individual attempt (including session-auth exchange
	// and response body read), not the retry chain. Zero uses the default (15s).
	Timeout time.Duration
	// Retry configures the retry middleware. Zero values use the retry defaults.
	Retry RetryOptions
	// MaxAuthFailures configures the circuit breaker. Zero uses its default (5).
	MaxAuthFailures int
	// Auth is an optional authentication middleware (e.g. Bearer or SessionAuth).
	// Pass nil for upstreams that need no authentication.
	Auth Middleware
	// Debug configures the DebugLog middleware, placed innermost when enabled.
	// Disabled by default; development use only. An empty Debug.Name falls
	// back to Name.
	Debug DebugLogOptions
}

// NewClient builds an *http.Client applying the canonical resilience chain,
// outermost first: CircuitBreaker → Retry → AttemptTimeout → Auth → DebugLog.
// Every upstream adapter uses this path; adapters supply only their auth
// middleware. The timeout sits inside Retry (per-attempt, not whole-chain), so
// the client sets no http.Client.Timeout.
func NewClient(opts ClientOptions) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultAttemptTimeout
	}

	mws := []Middleware{
		CircuitBreaker(CircuitBreakerOptions{
			Name:            opts.Name,
			MaxAuthFailures: opts.MaxAuthFailures,
		}),
		Retry(opts.Retry),
		AttemptTimeout(timeout),
	}
	if opts.Auth != nil {
		mws = append(mws, opts.Auth)
	}
	if opts.Debug.Enabled {
		debug := opts.Debug
		if debug.Name == "" {
			debug.Name = opts.Name
		}
		mws = append(mws, DebugLog(debug))
	}

	return &http.Client{
		Transport: Chain(nil, mws...),
	}
}

// Middleware wraps an http.RoundTripper with additional behavior.
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

// drainAndClose discards the remaining response body and closes it so the
// underlying connection can be reused. Safe to call with nil.
func drainAndClose(resp *http.Response) {
	if resp == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

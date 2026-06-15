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

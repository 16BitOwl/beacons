package transport

import (
	"errors"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

const (
	defaultMaxAttempts = 3
	defaultBaseDelay   = 500 * time.Millisecond
	defaultMaxDelay    = 30 * time.Second
)

// RetryOptions configures retry behavior.
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
// On HTTP 429, the Retry-After response header is honored if present, capped
// at MaxDelay.
// Errors wrapping [ErrAuthFailed] are permanent and never retried.
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
			if req.Body != nil && req.GetBody == nil {
				// Body cannot be replayed; return whatever the last attempt gave us.
				return resp, err
			}
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
			if errors.Is(err, ErrAuthFailed) {
				// Rejected credentials won't heal with a retry; let the error
				// propagate so the circuit breaker can count it.
				return nil, err
			}
			continue
		}
		if !isRetryable(resp.StatusCode) {
			return resp, nil
		}
	}
	return resp, err
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
// It honors the Retry-After header on 429 responses when available, capped at
// opts.MaxDelay.
func calcDelay(attempt int, resp *http.Response, opts RetryOptions) time.Duration {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			// Clamp to MaxDelay: a hostile/huge Retry-After must not stall the syncer.
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				if d := time.Duration(secs) * time.Second; d < opts.MaxDelay {
					return d
				}
				return opts.MaxDelay
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

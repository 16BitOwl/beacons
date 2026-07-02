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
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const defaultAttemptTimeout = 15 * time.Second

// ClientOptions configures NewClient.
type ClientOptions struct {
	// Name identifies the upstream in circuit-breaker log messages. Optional.
	Name string
	// Timeout bounds each individual attempt — including any session-auth
	// exchange and reading the response body — not the retry chain as a whole.
	// Backoff sleeps between attempts are therefore never charged against it.
	// Zero uses the default (15s).
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

// NewClient builds an *http.Client whose transport applies the canonical
// resilience middleware in order: CircuitBreaker (outermost) → Retry →
// AttemptTimeout → Auth → DebugLog (when enabled).
//
// This is the single construction path every upstream adapter should use so
// that retry, backoff, and circuit-breaking behaviour is identical across
// providers. Adapters only supply their authentication middleware.
//
// The timeout sits inside Retry so each attempt gets a fresh deadline; the
// returned client deliberately has no http.Client.Timeout, which would cap the
// entire retry chain including backoff sleeps. Total time is still bounded by
// MaxAttempts × (Timeout + MaxDelay).
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
// AttemptTimeout middleware
// ---------------------------------------------------------------------------

// AttemptTimeout returns a Middleware that applies timeout to every request
// passing through it, covering the attempt itself and reading its response
// body. Place it inside Retry (after it in the Chain call) so each retry
// attempt gets a fresh deadline — unlike http.Client.Timeout, which spans the
// whole chain including backoff sleeps.
func AttemptTimeout(timeout time.Duration) Middleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &attemptTimeoutTransport{next: next, timeout: timeout}
	}
}

type attemptTimeoutTransport struct {
	next    http.RoundTripper
	timeout time.Duration
}

func (t *attemptTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.timeout)
	resp, err := t.next.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	// Keep the deadline active while the caller consumes the body; release the
	// context as soon as the body is closed.
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnCloseBody cancels its context when the response body is closed, so
// the attempt deadline covers body reads without leaking the context.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
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
// SessionAuth middleware
// ---------------------------------------------------------------------------

const defaultSessionEarlyExpiry = 30 * time.Second

// defaultSessionTTL is used when an Authenticator reports a non-positive
// ExpiresIn (e.g. an upstream that requires no authentication). The token is
// still cached for this long so it is not re-acquired on every request.
const defaultSessionTTL = 30 * time.Minute

// Session is the result of an authentication exchange.
type Session struct {
	// Token is the value placed in the configured auth header. It may be empty
	// when the upstream requires no authentication; the header is then omitted.
	Token string
	// ExpiresIn is how long the token remains valid. A non-positive value means
	// "unknown / no expiry" and falls back to a default caching window.
	ExpiresIn time.Duration
}

// ErrAuthFailed marks a permanent authentication failure: the upstream rejected
// the credentials themselves, not just an expired token. Authenticator
// implementations must wrap it (fmt.Errorf("...: %w", transport.ErrAuthFailed))
// when the auth endpoint rejects the configured credentials, so that Retry does
// not retry the error and CircuitBreaker counts it towards disabling the
// upstream. Detect it with errors.Is.
var ErrAuthFailed = errors.New("transport: authentication failed")

// Authenticator acquires a session token. It is invoked by SessionAuth on first
// use and whenever the cached token is rejected with HTTP 401. Implementations
// must perform their own HTTP exchange using a client that does NOT route back
// through the SessionAuth middleware, to avoid recursion. Permanent credential
// rejections must be wrapped with [ErrAuthFailed].
type Authenticator func(ctx context.Context) (Session, error)

// SessionAuthOptions configures the SessionAuth middleware.
type SessionAuthOptions struct {
	// Header is the request header the token is written to (e.g. "X-FTL-SID").
	Header string
	// Authenticate acquires (and re-acquires) the session token. Required.
	Authenticate Authenticator
	// EarlyExpiry refreshes the token this long before its reported expiry to
	// avoid races with server-side expiry. Zero uses the default (30s).
	EarlyExpiry time.Duration
}

// SessionAuth returns a Middleware that manages a cached session token.
//
// It acquires a token via the Authenticator, caches it until shortly before it
// expires, and sets it on the configured request header. If a request comes
// back with HTTP 401, it invalidates the cached token, re-authenticates once,
// and retries the request a single time. A request whose body cannot be
// replayed (GetBody == nil) is not retried.
//
// Place SessionAuth innermost in the chain (closest to the base transport) so a
// persistent 401 — one that survives re-authentication — still propagates
// outward to Retry (which ignores it) and CircuitBreaker (which counts it).
func SessionAuth(opts SessionAuthOptions) Middleware {
	if opts.EarlyExpiry <= 0 {
		opts.EarlyExpiry = defaultSessionEarlyExpiry
	}
	return func(next http.RoundTripper) http.RoundTripper {
		return &sessionAuthTransport{next: next, opts: opts}
	}
}

type sessionAuthTransport struct {
	next http.RoundTripper
	opts SessionAuthOptions

	mu        sync.Mutex
	token     string
	expiresAt time.Time
	haveToken bool
}

func (t *sessionAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.ensureToken(req.Context(), false)
	if err != nil {
		return nil, err
	}

	resp, err := t.attempt(req, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// The token was rejected. We can only retry if the body is replayable.
	if req.Body != nil && req.GetBody == nil {
		return resp, nil
	}

	drainAndClose(resp)
	token, err = t.ensureToken(req.Context(), true)
	if err != nil {
		return nil, err
	}
	return t.attempt(req, token)
}

// attempt clones req, replays its body when possible, sets the auth header, and
// forwards it to the next transport. Cloning keeps the caller's request intact
// across the two attempts.
func (t *sessionAuthTransport) attempt(req *http.Request, token string) (*http.Response, error) {
	r := req.Clone(req.Context())
	if req.Body != nil && req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	if token != "" {
		r.Header.Set(t.opts.Header, token)
	}
	return t.next.RoundTrip(r)
}

// ensureToken returns a valid cached token, acquiring a new one when the cache
// is empty, expired, or forceRefresh is set.
func (t *sessionAuthTransport) ensureToken(ctx context.Context, forceRefresh bool) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !forceRefresh && t.haveToken && time.Now().Before(t.expiresAt) {
		return t.token, nil
	}

	sess, err := t.opts.Authenticate(ctx)
	if err != nil {
		t.haveToken = false
		return "", err
	}

	ttl := sess.ExpiresIn
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	// Refresh early, but never set an already-expired window for short TTLs.
	if ttl > t.opts.EarlyExpiry {
		ttl -= t.opts.EarlyExpiry
	}
	t.token = sess.Token
	t.expiresAt = time.Now().Add(ttl)
	t.haveToken = true
	return t.token, nil
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

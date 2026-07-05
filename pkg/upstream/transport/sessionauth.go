package transport

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

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

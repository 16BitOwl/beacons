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

// ErrAuthFailed marks a permanent credential rejection (not an expired token).
// Authenticators must wrap it with %w so Retry skips it and CircuitBreaker
// counts it. Detect with errors.Is.
var ErrAuthFailed = errors.New("transport: authentication failed")

// Authenticator acquires a session token, invoked on first use and on HTTP 401.
// Implementations must use a client that does NOT route back through SessionAuth
// (avoids recursion) and wrap permanent rejections with [ErrAuthFailed].
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

// SessionAuth returns a Middleware that caches a session token from the
// Authenticator, sets it on the configured header, and on HTTP 401
// re-authenticates once and retries a single time (unless the body is
// non-replayable). Concurrent 401s off the same token share one
// re-authentication via the generation counter. Place it innermost so a
// persistent 401 still reaches Retry and CircuitBreaker.
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
	// generation counts successful authentications. It starts at 0 (no token
	// yet); real generations are always >= 1, so 0 doubles as the "don't force
	// a refresh" sentinel passed from the first ensureToken call in RoundTrip.
	generation uint64
}

func (t *sessionAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, gen, err := t.ensureToken(req.Context(), 0)
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
	token, _, err = t.ensureToken(req.Context(), gen)
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
// is empty or expired.
//
// invalidateGen is 0 for a plain cache lookup. Otherwise it is the generation
// the caller's token was rejected on; a refresh is only performed if the
// cache is still on that generation. If another goroutine already refreshed
// (the cache has moved to a later generation), the newer cached token is
// returned as-is — this is what collapses concurrent 401s into one
// Authenticate call instead of one per caller.
func (t *sessionAuthTransport) ensureToken(ctx context.Context, invalidateGen uint64) (string, uint64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if invalidateGen == 0 {
		if t.haveToken && time.Now().Before(t.expiresAt) {
			return t.token, t.generation, nil
		}
	} else if invalidateGen != t.generation {
		return t.token, t.generation, nil
	}

	sess, err := t.opts.Authenticate(ctx)
	if err != nil {
		t.haveToken = false
		return "", t.generation, err
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
	t.generation++
	return t.token, t.generation, nil
}

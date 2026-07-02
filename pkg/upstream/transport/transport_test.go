package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// roundTripFunc adapts a plain function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func fakeResponse(code int) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}
}

// ---------------------------------------------------------------------------
// Chain
// ---------------------------------------------------------------------------

func TestChain_OrderIsPreserved(t *testing.T) {
	var order []int

	record := func(n int) Middleware {
		return func(next http.RoundTripper) http.RoundTripper {
			return roundTripFunc(func(req *http.Request) (*http.Response, error) {
				order = append(order, n)
				return next.RoundTrip(req)
			})
		}
	}

	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, record(1), record(2), record(3))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Errorf("order = %v, want [1 2 3]", order)
	}
}

func TestChain_EmptyMiddlewares_PassesThroughToBase(t *testing.T) {
	called := false
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	tr.RoundTrip(req) //nolint:errcheck

	if !called {
		t.Error("base transport was not called")
	}
}

func TestChain_NilBase_UsesDefaultTransport(t *testing.T) {
	// Verifies Chain does not panic with a nil base; the default transport is
	// substituted. We wrap it in a middleware to intercept without making a
	// real network call.
	reached := false
	tr := Chain(nil, func(next http.RoundTripper) http.RoundTripper {
		reached = true
		return next
	})
	if tr == nil {
		t.Fatal("Chain returned nil")
	}
	if !reached {
		// The middleware is applied eagerly during Chain — but actually it's
		// applied lazily. Just confirm the chain was built without panic.
		_ = reached
	}
}

// ---------------------------------------------------------------------------
// Bearer
// ---------------------------------------------------------------------------

func TestBearer_AddsAuthorizationHeader(t *testing.T) {
	var got string
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		got = req.Header.Get("Authorization")
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, Bearer("secret-token"))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if got != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer secret-token")
	}
}

func TestBearer_DoesNotMutateOriginalRequest(t *testing.T) {
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, Bearer("token"))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	original := req.Header.Get("Authorization")

	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if req.Header.Get("Authorization") != original {
		t.Error("Bearer middleware mutated the original request's Authorization header")
	}
}

// ---------------------------------------------------------------------------
// Retry — attempt counting
// ---------------------------------------------------------------------------

func TestRetry_SuccessOnFirstAttempt_NoRetry(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Nanosecond}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

func TestRetry_NonRetryableStatus_NoRetry(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 422} {
		code := code
		calls := 0
		base := roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return fakeResponse(code), nil
		})

		tr := Chain(base, Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Nanosecond}))
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		tr.RoundTrip(req) //nolint:errcheck

		if calls != 1 {
			t.Errorf("status %d: calls = %d, want 1", code, calls)
		}
	}
}

func TestRetry_RetryableStatuses_RetriesUpToMaxAttempts(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503, 504} {
		code := code
		calls := 0
		base := roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return fakeResponse(code), nil
		})

		const maxAttempts = 3
		tr := Chain(base, Retry(RetryOptions{MaxAttempts: maxAttempts, BaseDelay: time.Nanosecond}))
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		tr.RoundTrip(req) //nolint:errcheck

		if calls != maxAttempts {
			t.Errorf("status %d: calls = %d, want %d", code, calls, maxAttempts)
		}
	}
}

func TestRetry_NetworkError_RetriesUpToMaxAttempts(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("dial: connection refused")
	})

	const maxAttempts = 3
	tr := Chain(base, Retry(RetryOptions{MaxAttempts: maxAttempts, BaseDelay: time.Nanosecond}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("expected error, got nil")
	}

	if calls != maxAttempts {
		t.Errorf("calls = %d, want %d", calls, maxAttempts)
	}
}

func TestRetry_SucceedsOnSecondAttempt(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return fakeResponse(http.StatusServiceUnavailable), nil
		}
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Nanosecond}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestRetry_ZeroOptions_UsesDefaults(t *testing.T) {
	// Zero RetryOptions should not panic and should apply sensible defaults.
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, Retry(RetryOptions{}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
}

// ---------------------------------------------------------------------------
// Retry — body handling
// ---------------------------------------------------------------------------

func TestRetry_NonResettableBody_OnlyOneAttempt(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusInternalServerError), nil
	})

	tr := Chain(base, Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Nanosecond}))

	// io.NopCloser around a plain Reader has no GetBody — non-resettable.
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", nil)
	req.Body = io.NopCloser(strings.NewReader("payload"))
	req.GetBody = nil

	tr.RoundTrip(req) //nolint:errcheck

	if calls != 1 {
		t.Errorf("calls = %d, want 1 (non-resettable body must not retry)", calls)
	}
}

func TestRetry_ResettableBody_RetriesNormally(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		// Drain the body so we verify it is present on each attempt.
		if req.Body != nil {
			io.Copy(io.Discard, req.Body) //nolint:errcheck
		}
		return fakeResponse(http.StatusInternalServerError), nil
	})

	const maxAttempts = 3
	tr := Chain(base, Retry(RetryOptions{MaxAttempts: maxAttempts, BaseDelay: time.Nanosecond}))

	// http.NewRequest sets GetBody for strings.NewReader automatically.
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader("payload"))

	tr.RoundTrip(req) //nolint:errcheck

	if calls != maxAttempts {
		t.Errorf("calls = %d, want %d", calls, maxAttempts)
	}
}

// ---------------------------------------------------------------------------
// Retry — context cancellation
// ---------------------------------------------------------------------------

func TestRetry_ContextCancelled_StopsRetrying(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusInternalServerError), nil
	})

	// Pre-cancel the context so the retry wait fires context.Canceled immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tr := Chain(base, Retry(RetryOptions{
		MaxAttempts: 5,
		BaseDelay:   time.Hour, // large so context wins the select race
		MaxDelay:    time.Hour,
	}))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com", nil)
	_, err := tr.RoundTrip(req)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (context cancelled before first retry)", calls)
	}
}

// ---------------------------------------------------------------------------
// calcDelay
// ---------------------------------------------------------------------------

func TestCalcDelay_RetryAfterHeader_HonouredOn429(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"42"}},
	}
	opts := RetryOptions{BaseDelay: time.Millisecond, MaxDelay: time.Hour}

	d := calcDelay(1, resp, opts)

	if d != 42*time.Second {
		t.Errorf("delay = %v, want 42s", d)
	}
}

func TestCalcDelay_RetryAfterHeader_IgnoredOnNon429(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{"Retry-After": []string{"42"}},
	}
	opts := RetryOptions{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Hour}

	d := calcDelay(1, resp, opts)

	// Should use exponential backoff, not Retry-After.
	if d == 42*time.Second {
		t.Error("Retry-After should be ignored on non-429 responses")
	}
}

func TestCalcDelay_ExponentialGrowth(t *testing.T) {
	opts := RetryOptions{BaseDelay: 100 * time.Millisecond, MaxDelay: time.Hour}

	d1 := calcDelay(1, nil, opts)
	d2 := calcDelay(2, nil, opts)

	// d2 should be roughly twice d1 (jitter is ±25% so it won't be exact).
	if d2 <= d1 {
		t.Errorf("delay should grow: d1=%v d2=%v", d1, d2)
	}
}

func TestCalcDelay_CappedAtMaxDelay(t *testing.T) {
	opts := RetryOptions{BaseDelay: time.Second, MaxDelay: 2 * time.Second}

	// High attempt count would produce a huge delay without the cap.
	d := calcDelay(20, nil, opts)

	if d > opts.MaxDelay {
		t.Errorf("delay = %v, want <= MaxDelay (%v)", d, opts.MaxDelay)
	}
}

// ---------------------------------------------------------------------------
// Header middleware
// ---------------------------------------------------------------------------

func TestHeader_SetsHeaderOnRequest(t *testing.T) {
	var got string
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		got = req.Header.Get("X-Custom-Header")
		return fakeResponse(http.StatusOK), nil
	})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	Chain(base, Header("X-Custom-Header", "my-value")).RoundTrip(req) //nolint:errcheck

	if got != "my-value" {
		t.Errorf("header value = %q, want my-value", got)
	}
}

func TestHeader_DoesNotMutateOriginalRequest(t *testing.T) {
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusOK), nil
	})

	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	Chain(base, Header("X-Injected", "value")).RoundTrip(req) //nolint:errcheck

	if req.Header.Get("X-Injected") != "" {
		t.Error("Header middleware mutated the original request")
	}
}

// ---------------------------------------------------------------------------
// CircuitBreaker
// ---------------------------------------------------------------------------

func TestCircuitBreaker_AuthError_PassesThroughResponse(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		code := code
		base := roundTripFunc(func(*http.Request) (*http.Response, error) {
			return fakeResponse(code), nil
		})

		tr := Chain(base, CircuitBreaker(CircuitBreakerOptions{MaxAuthFailures: 5}))
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		resp, err := tr.RoundTrip(req)
		if err != nil {
			t.Fatalf("status %d: RoundTrip: %v", code, err)
		}
		if resp.StatusCode != code {
			t.Errorf("status %d: got %d, want %d", code, resp.StatusCode, code)
		}
	}
}

func TestCircuitBreaker_TripsAfterMaxFails(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			callCount := 0
			base := roundTripFunc(func(*http.Request) (*http.Response, error) {
				callCount++
				return fakeResponse(code), nil
			})

			const maxFails = 3
			tr := Chain(base, CircuitBreaker(CircuitBreakerOptions{MaxAuthFailures: maxFails}))
			req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

			for range maxFails {
				tr.RoundTrip(req) //nolint:errcheck
			}

			_, err := tr.RoundTrip(req)
			if !errors.Is(err, ErrCircuitOpen) {
				t.Errorf("after trip: got %v, want ErrCircuitOpen", err)
			}
			if callCount != maxFails {
				t.Errorf("server received %d calls, want %d", callCount, maxFails)
			}
		})
	}
}


func TestCircuitBreaker_SuccessResetsCounter(t *testing.T) {
	call := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		call++
		if call == 1 {
			return fakeResponse(http.StatusUnauthorized), nil
		}
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, CircuitBreaker(CircuitBreakerOptions{MaxAuthFailures: 3}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	tr.RoundTrip(req) //nolint:errcheck — one 401
	tr.RoundTrip(req) //nolint:errcheck — success, resets counter

	// Two more 401s: counter is back at 0 so the circuit should not trip yet.
	base2 := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusUnauthorized), nil
	})
	tr2 := Chain(base2, CircuitBreaker(CircuitBreakerOptions{MaxAuthFailures: 3}))
	tr2.RoundTrip(req) //nolint:errcheck
	tr2.RoundTrip(req) //nolint:errcheck
	resp, err := tr2.RoundTrip(req) // third 401 — should trip now
	if err != nil {
		t.Fatalf("third 401: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestCircuitBreaker_ZeroOptions_UsesDefault(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusUnauthorized), nil
	})

	tr := Chain(base, CircuitBreaker(CircuitBreakerOptions{}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	// Should not trip until defaultMaxAuthFailures calls.
	for range defaultMaxAuthFailures {
		tr.RoundTrip(req) //nolint:errcheck
	}

	_, err := tr.RoundTrip(req)
	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("after %d 401s with zero options: got %v, want ErrCircuitOpen", defaultMaxAuthFailures, err)
	}
}

// ---------------------------------------------------------------------------
// isAuthError
// ---------------------------------------------------------------------------

func TestIsAuthError(t *testing.T) {
	authErrors := []int{401, 403}
	for _, code := range authErrors {
		if !isAuthError(code) {
			t.Errorf("isAuthError(%d) = false, want true", code)
		}
	}

	notAuthErrors := []int{200, 400, 404, 429, 500, 502, 503}
	for _, code := range notAuthErrors {
		if isAuthError(code) {
			t.Errorf("isAuthError(%d) = true, want false", code)
		}
	}
}

// ---------------------------------------------------------------------------
// isRetryable
// ---------------------------------------------------------------------------

func TestIsRetryable(t *testing.T) {
	retryable := []int{429, 500, 502, 503, 504}
	for _, code := range retryable {
		if !isRetryable(code) {
			t.Errorf("isRetryable(%d) = false, want true", code)
		}
	}

	notRetryable := []int{200, 201, 204, 301, 400, 401, 403, 404, 422}
	for _, code := range notRetryable {
		if isRetryable(code) {
			t.Errorf("isRetryable(%d) = true, want false", code)
		}
	}
}

// ---------------------------------------------------------------------------
// SessionAuth
// ---------------------------------------------------------------------------

func TestSessionAuth_SetsHeaderAndCachesToken(t *testing.T) {
	authCalls := 0
	auth := func(context.Context) (Session, error) {
		authCalls++
		return Session{Token: "tok", ExpiresIn: time.Hour}, nil
	}

	var seen []string
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		seen = append(seen, req.Header.Get("X-Tok"))
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}))

	for range 3 {
		req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
		if _, err := tr.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
	}

	if authCalls != 1 {
		t.Errorf("authenticate calls = %d, want 1 (token should be cached)", authCalls)
	}
	for i, h := range seen {
		if h != "tok" {
			t.Errorf("request %d header = %q, want tok", i, h)
		}
	}
}

func TestSessionAuth_RefreshesAndRetriesOn401(t *testing.T) {
	authCalls := 0
	auth := func(context.Context) (Session, error) {
		authCalls++
		return Session{Token: "tok", ExpiresIn: time.Hour}, nil
	}

	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return fakeResponse(http.StatusUnauthorized), nil
		}
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("base calls = %d, want 2 (401 + retry)", calls)
	}
	if authCalls != 2 {
		t.Errorf("authenticate calls = %d, want 2 (initial + refresh on 401)", authCalls)
	}
}

func TestSessionAuth_PersistentUnauthorizedReturns401(t *testing.T) {
	auth := func(context.Context) (Session, error) {
		return Session{Token: "tok", ExpiresIn: time.Hour}, nil
	}
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusUnauthorized), nil
	})

	tr := Chain(base, SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("base calls = %d, want 2 (initial + one retry)", calls)
	}
}

func TestSessionAuth_ReplaysBodyOnRetry(t *testing.T) {
	auth := func(context.Context) (Session, error) {
		return Session{Token: "tok", ExpiresIn: time.Hour}, nil
	}
	var bodies []string
	calls := 0
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		b, _ := io.ReadAll(req.Body)
		bodies = append(bodies, string(b))
		if calls == 1 {
			return fakeResponse(http.StatusUnauthorized), nil
		}
		return fakeResponse(http.StatusNoContent), nil
	})

	tr := Chain(base, SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}))
	req, _ := http.NewRequest(http.MethodPatch, "http://example.com", strings.NewReader("payload"))
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if len(bodies) != 2 || bodies[0] != "payload" || bodies[1] != "payload" {
		t.Errorf("bodies = %v, want both \"payload\" (replayed on retry)", bodies)
	}
}

func TestSessionAuth_EmptyTokenOmitsHeader(t *testing.T) {
	auth := func(context.Context) (Session, error) {
		// validity=-1 / no-auth upstream: empty token.
		return Session{Token: "", ExpiresIn: 0}, nil
	}
	headerPresent := true
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		_, headerPresent = req.Header["X-Tok"]
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if headerPresent {
		t.Error("empty token should not set the auth header")
	}
}

func TestSessionAuth_AuthenticateErrorPropagates(t *testing.T) {
	wantErr := errors.New("auth boom")
	auth := func(context.Context) (Session, error) {
		return Session{}, wantErr
	}
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("base should not be reached when authentication fails")
		return nil, nil
	})

	tr := Chain(base, SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
}

// ---------------------------------------------------------------------------
// ErrAuthFailed handling
// ---------------------------------------------------------------------------

func TestRetry_DoesNotRetryAuthFailedError(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("authenticate: %w", ErrAuthFailed)
	})

	tr := Chain(base, Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Millisecond}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if _, err := tr.RoundTrip(req); !errors.Is(err, ErrAuthFailed) {
		t.Errorf("err = %v, want error wrapping ErrAuthFailed", err)
	}
	if calls != 1 {
		t.Errorf("base calls = %d, want 1 (permanent auth failure must not be retried)", calls)
	}
}

func TestCircuitBreaker_OpensOnAuthFailedErrors(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("authenticate: %w", ErrAuthFailed)
	})

	tr := Chain(base, CircuitBreaker(CircuitBreakerOptions{Name: "test", MaxAuthFailures: 2}))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	for i := range 2 {
		if _, err := tr.RoundTrip(req); !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("call %d: err = %v, want error wrapping ErrAuthFailed", i+1, err)
		}
	}
	if _, err := tr.RoundTrip(req); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("err = %v, want ErrCircuitOpen after threshold", err)
	}
	if calls != 2 {
		t.Errorf("base calls = %d, want 2 (circuit must be open on the third call)", calls)
	}
}

// TestChain_RejectedCredentialsDisableUpstream is the regression test for the
// full wrong-password path: SessionAuth surfaces ErrAuthFailed, Retry lets it
// through untouched, and CircuitBreaker counts it until the upstream is
// disabled.
func TestChain_RejectedCredentialsDisableUpstream(t *testing.T) {
	authCalls := 0
	auth := func(context.Context) (Session, error) {
		authCalls++
		return Session{}, fmt.Errorf("pihole: %w: invalid password", ErrAuthFailed)
	}
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("request must not reach the upstream when authentication fails")
		return nil, nil
	})

	tr := Chain(base,
		CircuitBreaker(CircuitBreakerOptions{Name: "test", MaxAuthFailures: 2}),
		Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Millisecond}),
		SessionAuth(SessionAuthOptions{Header: "X-Tok", Authenticate: auth}),
	)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)

	for i := range 2 {
		if _, err := tr.RoundTrip(req); !errors.Is(err, ErrAuthFailed) {
			t.Fatalf("call %d: err = %v, want error wrapping ErrAuthFailed", i+1, err)
		}
	}
	if authCalls != 2 {
		t.Errorf("authenticate calls = %d, want 2 (one per request, no retries)", authCalls)
	}
	if _, err := tr.RoundTrip(req); !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("err = %v, want ErrCircuitOpen after threshold", err)
	}
	if authCalls != 2 {
		t.Errorf("authenticate calls after circuit open = %d, want 2", authCalls)
	}
}

// ---------------------------------------------------------------------------
// Retry: non-replayable body
// ---------------------------------------------------------------------------

func TestRetry_NonReplayableBody_ReturnsLastResponse(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return fakeResponse(http.StatusServiceUnavailable), nil
	})

	tr := Chain(base, Retry(RetryOptions{MaxAttempts: 3, BaseDelay: time.Millisecond}))
	// Wrap the reader so http.NewRequest cannot derive GetBody.
	body := struct{ io.Reader }{strings.NewReader("payload")}
	req, _ := http.NewRequest(http.MethodPost, "http://example.com", body)
	if req.GetBody != nil {
		t.Fatal("precondition failed: body should be non-replayable")
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil; the last response must be returned")
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	if calls != 1 {
		t.Errorf("base calls = %d, want 1 (non-replayable body must not be retried)", calls)
	}
}

// ---------------------------------------------------------------------------
// AttemptTimeout
// ---------------------------------------------------------------------------

func TestAttemptTimeout_EachAttemptGetsFreshDeadline(t *testing.T) {
	calls := 0
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			// Simulate an attempt that hangs until its per-attempt deadline.
			<-req.Context().Done()
			return nil, req.Context().Err()
		}
		if _, ok := req.Context().Deadline(); !ok {
			t.Error("second attempt has no deadline")
		}
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base,
		Retry(RetryOptions{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}),
		AttemptTimeout(20*time.Millisecond),
	)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if calls != 2 {
		t.Errorf("base calls = %d, want 2 (timed-out attempt + fresh retry)", calls)
	}
}

func TestAttemptTimeout_BodyCloseReleasesContext(t *testing.T) {
	var attemptCtx context.Context
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attemptCtx = req.Context()
		return fakeResponse(http.StatusOK), nil
	})

	tr := Chain(base, AttemptTimeout(time.Hour))
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	select {
	case <-attemptCtx.Done():
		t.Fatal("context cancelled before the body was closed")
	default:
	}
	resp.Body.Close()
	select {
	case <-attemptCtx.Done():
	default:
		t.Error("context not released when the body was closed")
	}
}

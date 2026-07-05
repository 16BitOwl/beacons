package transport

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

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

	tr.RoundTrip(req) //nolint:errcheck // one 401
	tr.RoundTrip(req) //nolint:errcheck // success, resets counter

	// Two more 401s: counter is back at 0 so the circuit should not trip yet.
	base2 := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return fakeResponse(http.StatusUnauthorized), nil
	})
	tr2 := Chain(base2, CircuitBreaker(CircuitBreakerOptions{MaxAuthFailures: 3}))
	tr2.RoundTrip(req)              //nolint:errcheck
	tr2.RoundTrip(req)              //nolint:errcheck
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

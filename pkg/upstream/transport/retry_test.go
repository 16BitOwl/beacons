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
// Retry — context cancellation
// ---------------------------------------------------------------------------

func TestRetry_ContextCanceled_StopsRetrying(t *testing.T) {
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
		t.Errorf("calls = %d, want 1 (context canceled before first retry)", calls)
	}
}

// ---------------------------------------------------------------------------
// Retry — ErrAuthFailed handling
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
// calcDelay
// ---------------------------------------------------------------------------

func TestCalcDelay_RetryAfterHeader_HonoredOn429(t *testing.T) {
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

func TestCalcDelay_RetryAfterHeader_CappedAtMaxDelay(t *testing.T) {
	// A hostile/huge Retry-After must not stall the syncer past MaxDelay.
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"86400"}},
	}
	opts := RetryOptions{BaseDelay: time.Millisecond, MaxDelay: 30 * time.Second}

	d := calcDelay(1, resp, opts)

	if d != opts.MaxDelay {
		t.Errorf("delay = %v, want %v (clamped)", d, opts.MaxDelay)
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

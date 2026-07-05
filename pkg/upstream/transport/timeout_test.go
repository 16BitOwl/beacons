package transport

import (
	"context"
	"net/http"
	"testing"
	"time"
)

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
		t.Fatal("context canceled before the body was closed")
	default:
	}
	resp.Body.Close()
	select {
	case <-attemptCtx.Done():
	default:
		t.Error("context not released when the body was closed")
	}
}

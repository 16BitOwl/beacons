package netutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// withEndpoints points publicIPEndpoints at a test server list for the
// duration of the calling test and clears the result cache before and after,
// so tests don't leak state or depend on real network access.
func withEndpoints(t *testing.T, endpoints ...string) {
	t.Helper()
	orig := publicIPEndpoints
	publicIPEndpoints = endpoints
	resetPublicIPCache()
	t.Cleanup(func() {
		publicIPEndpoints = orig
		resetPublicIPCache()
	})
}

func resetPublicIPCache() {
	publicIPCache.mu.Lock()
	publicIPCache.ip = ""
	publicIPCache.fetched = time.Time{}
	publicIPCache.mu.Unlock()
}

func TestPublicIP_ReturnsEndpointResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.9\n"))
	}))
	defer srv.Close()
	withEndpoints(t, srv.URL)

	ip, err := PublicIP(context.Background())
	if err != nil {
		t.Fatalf("PublicIP: %v", err)
	}
	if ip != "203.0.113.9" {
		t.Errorf("got %q, want 203.0.113.9", ip)
	}
}

func TestPublicIP_FallsBackToNextEndpointOnFailure(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("198.51.100.4"))
	}))
	defer good.Close()
	withEndpoints(t, bad.URL, good.URL)

	ip, err := PublicIP(context.Background())
	if err != nil {
		t.Fatalf("PublicIP: %v", err)
	}
	if ip != "198.51.100.4" {
		t.Errorf("got %q, want 198.51.100.4", ip)
	}
}

func TestPublicIP_AllEndpointsFail_ReturnsError(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	withEndpoints(t, bad.URL)

	if _, err := PublicIP(context.Background()); err == nil {
		t.Error("expected error when all endpoints fail")
	}
}

func TestPublicIP_NonIPResponseBody_TreatedAsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-an-ip"))
	}))
	defer srv.Close()
	withEndpoints(t, srv.URL)

	if _, err := PublicIP(context.Background()); err == nil {
		t.Error("expected error for non-IP response body")
	}
}

func TestPublicIP_CachesResultWithinTTL(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte("192.0.2.77"))
	}))
	defer srv.Close()
	withEndpoints(t, srv.URL)

	if _, err := PublicIP(context.Background()); err != nil {
		t.Fatalf("PublicIP: %v", err)
	}
	if _, err := PublicIP(context.Background()); err != nil {
		t.Fatalf("PublicIP: %v", err)
	}
	if calls != 1 {
		t.Errorf("endpoint called %d times, want 1 (second call should hit cache)", calls)
	}
}

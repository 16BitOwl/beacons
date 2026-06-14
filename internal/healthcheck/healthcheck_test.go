package healthcheck_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/16bitowl/beacons/internal/healthcheck"
)

func TestCheck_HTTP200_ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := healthcheck.Check(srv.URL); err != nil {
		t.Errorf("expected nil error for 200 response, got: %v", err)
	}
}

func TestCheck_Non200_ReturnsError(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusNotFound} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
			}))
			defer srv.Close()

			if err := healthcheck.Check(srv.URL); err == nil {
				t.Errorf("expected error for status %d, got nil", code)
			}
		})
	}
}

func TestCheck_ConnectionRefused_ReturnsError(t *testing.T) {
	// Port 1 is generally not listening — connection will be refused quickly.
	err := healthcheck.Check("http://127.0.0.1:1")
	if err == nil {
		t.Error("expected error when server is not listening, got nil")
	}
}

func TestCheck_RequestsCorrectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_ = healthcheck.Check(srv.URL)
	if gotPath != "/healthz" {
		t.Errorf("request path = %q, want /healthz", gotPath)
	}
}

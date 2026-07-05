package transport

import (
	"net/http"
	"testing"
)

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

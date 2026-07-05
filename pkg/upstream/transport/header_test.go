package transport

import (
	"net/http"
	"testing"
)

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

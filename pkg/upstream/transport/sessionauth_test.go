package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

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

package pihole

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/16bitowl/beacons/pkg/upstream/transport"
)

// ---------------------------------------------------------------------------
// toggleEntry
// ---------------------------------------------------------------------------

func TestToggleEntry_Add_AppendsMissingEntry(t *testing.T) {
	entries := []string{"a", "b"}
	got := toggleEntry(entries, "c", false)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[2] != "c" {
		t.Errorf("last entry = %q, want c", got[2])
	}
}

func TestToggleEntry_Add_DoesNotDuplicateExistingEntry(t *testing.T) {
	// toggleEntry always appends even if the entry already exists;
	// the caller's idempotency check prevents redundant PATCH calls.
	// Here we verify the returned slice length is correct.
	entries := []string{"a", "b", "c"}
	got := toggleEntry(entries, "b", false)
	// existing entries minus "b" → ["a", "c"], then append "b" → ["a", "c", "b"]
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestToggleEntry_Remove_DeletesMatchingEntry(t *testing.T) {
	entries := []string{"1.2.3.4 web", "5.6.7.8 api", "9.0.1.2 db"}
	got := toggleEntry(entries, "5.6.7.8 api", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, e := range got {
		if e == "5.6.7.8 api" {
			t.Errorf("removed entry should not be present")
		}
	}
}

func TestToggleEntry_Remove_NonExistentEntry_NoChange(t *testing.T) {
	entries := []string{"a", "b"}
	got := toggleEntry(entries, "c", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestToggleEntry_Remove_AllMatchingRemoved(t *testing.T) {
	// Only exact matches are removed — verify no over-removal.
	entries := []string{"web,target", "web,target,300", "api,target"}
	got := toggleEntry(entries, "web,target", true)
	// "web,target,300" is not an exact match — should remain.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; entries: %v", len(got), got)
	}
}

func TestToggleEntry_EmptyList_Add(t *testing.T) {
	got := toggleEntry(nil, "new", false)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0] != "new" {
		t.Errorf("got[0] = %q, want new", got[0])
	}
}

func TestToggleEntry_EmptyList_Remove(t *testing.T) {
	got := toggleEntry(nil, "new", true)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestToggleEntry_PreservesOrder(t *testing.T) {
	entries := []string{"first", "second", "third"}
	got := toggleEntry(entries, "fourth", false)
	// first, second, third are preserved in order, fourth appended
	if got[0] != "first" || got[1] != "second" || got[2] != "third" || got[3] != "fourth" {
		t.Errorf("order not preserved: %v", got)
	}
}

// ---------------------------------------------------------------------------
// containsEntry
// ---------------------------------------------------------------------------

func TestContainsEntry_Present_ReturnsTrue(t *testing.T) {
	entries := []string{"a", "b", "c"}
	if !containsEntry(entries, "b") {
		t.Error("expected true for present entry")
	}
}

func TestContainsEntry_Absent_ReturnsFalse(t *testing.T) {
	entries := []string{"a", "b", "c"}
	if containsEntry(entries, "d") {
		t.Error("expected false for absent entry")
	}
}

func TestContainsEntry_EmptySlice_ReturnsFalse(t *testing.T) {
	if containsEntry(nil, "x") {
		t.Error("expected false for nil slice")
	}
}

func TestContainsEntry_PartialMatch_ReturnsFalse(t *testing.T) {
	// "web,target" must not match "web,target,300" — exact match only.
	if containsEntry([]string{"web,target,300"}, "web,target") {
		t.Error("expected false: partial prefix should not match")
	}
}

func TestContainsEntry_FirstEntry_ReturnsTrue(t *testing.T) {
	if !containsEntry([]string{"first", "second"}, "first") {
		t.Error("expected true for first entry")
	}
}

func TestContainsEntry_LastEntry_ReturnsTrue(t *testing.T) {
	if !containsEntry([]string{"first", "second", "last"}, "last") {
		t.Error("expected true for last entry")
	}
}

// ---------------------------------------------------------------------------
// 401 retry — get and patch
// ---------------------------------------------------------------------------

// authServerHandler is a reusable HTTP handler that:
//   - POST /api/auth: always returns a valid session
//   - other paths: behaviour is delegated to the provided handler
func newRetryServer(t *testing.T, inner http.Handler) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"session":{"valid":true,"sid":"tok","validity":1800}}`)
			return
		}
		inner.ServeHTTP(w, r)
	}))
}

func TestGet_RetriesOn401(t *testing.T) {
	hostsCount := 0
	srv := newRetryServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostsCount++
		if hostsCount == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"config":{"dns":{"hosts":[]}}}`)
	}))
	defer srv.Close()

	u := New(Options{Name: "test", BaseURL: srv.URL, Password: "pw"})
	if _, err := u.getHosts(context.Background()); err != nil {
		t.Fatalf("getHosts: %v", err)
	}
	if hostsCount != 2 {
		t.Errorf("host GET calls = %d, want 2 (one 401 + one retry)", hostsCount)
	}
}

func TestGet_PersistentUnauthorized_ReturnsError(t *testing.T) {
	srv := newRetryServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	u := New(Options{Name: "test", BaseURL: srv.URL, Password: "pw"})
	_, err := u.getHosts(context.Background())
	if err == nil {
		t.Fatal("expected error after persistent 401, got nil")
	}
}

func TestPatch_RetriesOn401(t *testing.T) {
	patchCount := 0
	srv := newRetryServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		patchCount++
		if patchCount == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	u := New(Options{Name: "test", BaseURL: srv.URL, Password: "pw"})
	if err := u.patch(context.Background(), map[string]any{"key": "val"}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if patchCount != 2 {
		t.Errorf("PATCH calls = %d, want 2 (one 401 + one retry)", patchCount)
	}
}

func TestPatch_PersistentUnauthorized_ReturnsError(t *testing.T) {
	srv := newRetryServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	u := New(Options{Name: "test", BaseURL: srv.URL, Password: "pw"})
	if err := u.patch(context.Background(), map[string]any{"key": "val"}); err == nil {
		t.Fatal("expected error after persistent 401, got nil")
	}
}

// ---------------------------------------------------------------------------
// authenticate
// ---------------------------------------------------------------------------

func TestAuthenticate_RejectedCredentials_WrapsErrAuthFailed(t *testing.T) {
	authCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"session":{"valid":false,"sid":"","validity":0,"message":"password incorrect"}}`)
	}))
	defer srv.Close()

	u := New(Options{Name: "test", BaseURL: srv.URL, Password: "wrong"})
	_, err := u.authenticate(context.Background())
	if !errors.Is(err, transport.ErrAuthFailed) {
		t.Errorf("err = %v, want error wrapping transport.ErrAuthFailed", err)
	}
	if authCalls != 1 {
		t.Errorf("auth endpoint calls = %d, want 1 (401 is not retryable)", authCalls)
	}
}

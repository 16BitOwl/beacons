package pihole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream/transport"
)

// ---------------------------------------------------------------------------
// applyByName — hosts ("IP hostname")
// ---------------------------------------------------------------------------

func TestApplyByName_Hosts_AddAppends(t *testing.T) {
	got, changed := applyByName([]string{"1.2.3.4 web"}, "api", "5.6.7.8 api", false, hostName)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if len(got) != 2 || got[1] != "5.6.7.8 api" {
		t.Errorf("got %v, want [1.2.3.4 web 5.6.7.8 api]", got)
	}
}

func TestApplyByName_Hosts_ValueChangeReplacesStale(t *testing.T) {
	// The #4 bug: changing an IP must not leave the old entry behind.
	got, changed := applyByName([]string{"1.2.3.4 web", "9.9.9.9 db"}, "web", "5.6.7.8 web", false, hostName)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	for _, e := range got {
		if e == "1.2.3.4 web" {
			t.Errorf("stale entry %q not removed: %v", e, got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (db preserved, web replaced): %v", len(got), got)
	}
}

func TestApplyByName_Hosts_UnchangedWhenIdentical(t *testing.T) {
	_, changed := applyByName([]string{"1.2.3.4 web"}, "web", "1.2.3.4 web", false, hostName)
	if changed {
		t.Error("changed = true, want false for identical entry")
	}
}

func TestApplyByName_Hosts_RemoveByName(t *testing.T) {
	got, changed := applyByName([]string{"1.2.3.4 web", "5.6.7.8 api"}, "web", "1.2.3.4 web", true, hostName)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if len(got) != 1 || got[0] != "5.6.7.8 api" {
		t.Errorf("got %v, want [5.6.7.8 api]", got)
	}
}

func TestApplyByName_Hosts_RemoveAbsent_NoChange(t *testing.T) {
	_, changed := applyByName([]string{"5.6.7.8 api"}, "web", "1.2.3.4 web", true, hostName)
	if changed {
		t.Error("changed = true, want false when name absent")
	}
}

func TestApplyByName_Hosts_PreservesUnrelatedOrder(t *testing.T) {
	got, _ := applyByName([]string{"1.1.1.1 a", "2.2.2.2 b"}, "c", "3.3.3.3 c", false, hostName)
	if got[0] != "1.1.1.1 a" || got[1] != "2.2.2.2 b" || got[2] != "3.3.3.3 c" {
		t.Errorf("order not preserved: %v", got)
	}
}

// ---------------------------------------------------------------------------
// applyByName — cname ("alias,target[,ttl]")
// ---------------------------------------------------------------------------

func TestApplyByName_CNAME_TTLChangeReplacesStale(t *testing.T) {
	got, changed := applyByName([]string{"web,target,300"}, "web", "web,target,600", false, cnameAlias)
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if len(got) != 1 || got[0] != "web,target,600" {
		t.Errorf("got %v, want [web,target,600]", got)
	}
}

func TestApplyByName_CNAME_UnchangedWhenIdentical(t *testing.T) {
	_, changed := applyByName([]string{"web,target,300"}, "web", "web,target,300", false, cnameAlias)
	if changed {
		t.Error("changed = true, want false for identical entry")
	}
}

// ---------------------------------------------------------------------------
// hostName / cnameAlias
// ---------------------------------------------------------------------------

func TestHostName(t *testing.T) {
	if got := hostName("1.2.3.4 web"); got != "web" {
		t.Errorf("single-host = %q, want web", got)
	}
	if got := hostName("::1 ipv6.host"); got != "ipv6.host" {
		t.Errorf("ipv6 = %q, want ipv6.host", got)
	}
	// Multi-host line returns the whole segment, so it won't match a single name.
	if got := hostName("1.2.3.4 a b"); got != "a b" {
		t.Errorf("multi-host = %q, want 'a b'", got)
	}
	if got := hostName("malformed"); got != "" {
		t.Errorf("malformed = %q, want empty", got)
	}
}

func TestCNAMEAlias(t *testing.T) {
	if got := cnameAlias("web,target"); got != "web" {
		t.Errorf("no-ttl = %q, want web", got)
	}
	if got := cnameAlias("web,target,300"); got != "web" {
		t.Errorf("with-ttl = %q, want web", got)
	}
	if got := cnameAlias("noComma"); got != "noComma" {
		t.Errorf("no-comma = %q, want noComma", got)
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
// Upsert flow — value change drops the stale hosts entry (#4)
// ---------------------------------------------------------------------------

func TestUpsert_HostsValueChange_PatchDropsStaleEntry(t *testing.T) {
	var patched map[string]any
	srv := newRetryServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			// Existing state still holds the old IP for web.
			fmt.Fprint(w, `{"config":{"dns":{"hosts":["1.2.3.4 web","9.9.9.9 db"]}}}`)
		case http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&patched)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	u := New(Options{Name: "test", BaseURL: srv.URL, Password: "pw"})
	rec := model.Record{Type: model.RecordTypeA, Name: "web", Value: "5.6.7.8"}
	if err := u.Upsert(context.Background(), rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	hosts := patchedHosts(t, patched)
	for _, h := range hosts {
		if h == "1.2.3.4 web" {
			t.Errorf("stale entry still present in PATCH: %v", hosts)
		}
	}
	if !contains(hosts, "5.6.7.8 web") {
		t.Errorf("new entry missing from PATCH: %v", hosts)
	}
	if !contains(hosts, "9.9.9.9 db") {
		t.Errorf("unrelated entry dropped from PATCH: %v", hosts)
	}
}

// patchedHosts extracts config.dns.hosts from a decoded PATCH body.
func patchedHosts(t *testing.T, body map[string]any) []string {
	t.Helper()
	cfg, _ := body["config"].(map[string]any)
	dns, _ := cfg["dns"].(map[string]any)
	raw, _ := dns["hosts"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
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

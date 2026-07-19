package technitium

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// tOK encodes a successful Technitium API response envelope.
func tOK(response any) string {
	b, _ := json.Marshal(map[string]any{
		"response": response,
		"status":   "ok",
	})
	return string(b)
}

// tErr encodes a failed Technitium API response envelope.
func tErr(msg string) string {
	return fmt.Sprintf(`{"status":"error","errorMessage":%q,"response":{}}`, msg)
}

// rec builds a minimal model.Record for use in tests.
func rec(rtype model.RecordType, name, value string, ttl, priority int) model.Record {
	return model.Record{
		BaseRecord: model.BaseRecord{TTL: ttl, Priority: priority},
		Type:       rtype,
		Name:       name,
		Value:      value,
	}
}

func newTestUpstream(baseURL, zone string) *Upstream {
	return &Upstream{
		name: "test",
		zone: zone,
		client: &tClient{
			http:    &http.Client{},
			baseURL: baseURL,
			zone:    zone,
		},
	}
}

// ---------------------------------------------------------------------------
// New / checkZone
// ---------------------------------------------------------------------------

func TestNew_ZoneFound_Succeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/zones/options/get" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok", r.Header.Get("Authorization"))
		}
		_, _ = fmt.Fprint(w, tOK(map[string]any{"name": "example.com", "type": "Primary"}))
	}))
	defer srv.Close()

	u, err := New(context.Background(), Options{Name: "test", BaseURL: srv.URL, APIToken: "tok", Zone: "example.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if u.Name() != "test" {
		t.Errorf("Name() = %q, want test", u.Name())
	}
}

func TestNew_ZoneNotFound_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, tErr("Zone does not exist."))
	}))
	defer srv.Close()

	_, err := New(context.Background(), Options{Name: "test", BaseURL: srv.URL, APIToken: "tok", Zone: "nope.com"})
	if err == nil {
		t.Fatal("expected error for missing zone, got nil")
	}
}

// ---------------------------------------------------------------------------
// Upsert — create
// ---------------------------------------------------------------------------

func TestUpsert_NoExisting_CreatesRecord(t *testing.T) {
	var addQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{"records": []any{}}))
		case strings.HasSuffix(r.URL.Path, "/records/add"):
			_ = r.ParseForm()
			addQuery = r.Form
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeA, "web", "1.2.3.4", 300, 0)
	if err := u.Upsert(context.Background(), r); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got := addQuery.Get("ipAddress"); got != "1.2.3.4" {
		t.Errorf("ipAddress = %q, want 1.2.3.4", got)
	}
	if got := addQuery.Get("domain"); got != "web" {
		t.Errorf("domain = %q, want web", got)
	}
	if got := addQuery.Get("zone"); got != "example.com" {
		t.Errorf("zone = %q, want example.com", got)
	}
}

// ---------------------------------------------------------------------------
// Upsert — update / no-op
// ---------------------------------------------------------------------------

func TestUpsert_ExistingDifferentValue_UpdatesRecord(t *testing.T) {
	var updateForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{
				"records": []any{
					map[string]any{
						"name": "web", "type": "A", "ttl": 300,
						"rData": map[string]any{"ipAddress": "1.2.3.4"},
					},
				},
			}))
		case strings.HasSuffix(r.URL.Path, "/records/update"):
			_ = r.ParseForm()
			updateForm = r.Form
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeA, "web", "5.6.7.8", 300, 0)
	if err := u.Upsert(context.Background(), r); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if got := updateForm.Get("ipAddress"); got != "1.2.3.4" {
		t.Errorf("ipAddress (old) = %q, want 1.2.3.4", got)
	}
	if got := updateForm.Get("newIpAddress"); got != "5.6.7.8" {
		t.Errorf("newIpAddress = %q, want 5.6.7.8", got)
	}
}

func TestUpsert_ExistingMatches_NoOpSkipsUpdate(t *testing.T) {
	updateCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{
				"records": []any{
					map[string]any{
						"name": "web", "type": "A", "ttl": 300,
						"rData": map[string]any{"ipAddress": "1.2.3.4"},
					},
				},
			}))
		case strings.HasSuffix(r.URL.Path, "/records/update"):
			updateCalled = true
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeA, "web", "1.2.3.4", 300, 0)
	if err := u.Upsert(context.Background(), r); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if updateCalled {
		t.Error("update called for a record that already matches")
	}
}

func TestUpsert_MX_ComparesExchangeAndPreference(t *testing.T) {
	updateCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{
				"records": []any{
					map[string]any{
						"name": "mail", "type": "MX", "ttl": 300,
						"rData": map[string]any{"exchange": "mx1.example.com", "preference": 10},
					},
				},
			}))
		case strings.HasSuffix(r.URL.Path, "/records/update"):
			updateCalled = true
			_ = r.ParseForm()
			if got := r.Form.Get("newPreference"); got != "20" {
				t.Errorf("newPreference = %q, want 20", got)
			}
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeMX, "mail", "mx1.example.com", 300, 20)
	if err := u.Upsert(context.Background(), r); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !updateCalled {
		t.Error("expected update call for preference change, none happened")
	}
}

// ---------------------------------------------------------------------------
// Unsupported types
// ---------------------------------------------------------------------------

func TestUpsert_UnsupportedType_SRV_ReturnsError(t *testing.T) {
	u := newTestUpstream("http://unused", "example.com")
	r := rec(model.RecordTypeSRV, "_svc._tcp", "target.example.com", 300, 10)
	if err := u.Upsert(context.Background(), r); err == nil {
		t.Fatal("expected error for SRV, got nil")
	}
}

func TestUpsert_UnsupportedType_CAA_ReturnsError(t *testing.T) {
	u := newTestUpstream("http://unused", "example.com")
	r := rec(model.RecordTypeCAA, "example.com", "letsencrypt.org", 300, 0)
	if err := u.Upsert(context.Background(), r); err == nil {
		t.Fatal("expected error for CAA, got nil")
	}
}

func TestDelete_UnsupportedType_SRV_ReturnsError(t *testing.T) {
	u := newTestUpstream("http://unused", "example.com")
	r := rec(model.RecordTypeSRV, "_svc._tcp", "target.example.com", 300, 10)
	if err := u.Delete(context.Background(), r); err == nil {
		t.Fatal("expected error for SRV, got nil")
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete_ExistingRecord_DeletesIt(t *testing.T) {
	var deleteForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{
				"records": []any{
					map[string]any{
						"name": "web", "type": "A", "ttl": 300,
						"rData": map[string]any{"ipAddress": "1.2.3.4"},
					},
				},
			}))
		case strings.HasSuffix(r.URL.Path, "/records/delete"):
			_ = r.ParseForm()
			deleteForm = r.Form
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeA, "web", "1.2.3.4", 300, 0)
	if err := u.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := deleteForm.Get("ipAddress"); got != "1.2.3.4" {
		t.Errorf("ipAddress = %q, want 1.2.3.4", got)
	}
}

func TestDelete_NotFound_SkipsWithoutError(t *testing.T) {
	deleteCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{"records": []any{}}))
		case strings.HasSuffix(r.URL.Path, "/records/delete"):
			deleteCalled = true
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeA, "web", "1.2.3.4", 300, 0)
	if err := u.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleteCalled {
		t.Error("delete called for a record that was not found")
	}
}

func TestDelete_CNAME_NoValueParamSent(t *testing.T) {
	var deleteForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/records/get"):
			_, _ = fmt.Fprint(w, tOK(map[string]any{
				"records": []any{
					map[string]any{
						"name": "alias", "type": "CNAME", "ttl": 300,
						"rData": map[string]any{"cname": "target.example.com"},
					},
				},
			}))
		case strings.HasSuffix(r.URL.Path, "/records/delete"):
			_ = r.ParseForm()
			deleteForm = r.Form
			_, _ = fmt.Fprint(w, tOK(map[string]any{}))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "example.com")
	r := rec(model.RecordTypeCNAME, "alias", "target.example.com", 300, 0)
	if err := u.Delete(context.Background(), r); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := deleteForm.Get("cname"); got != "" {
		t.Errorf("cname param = %q, want empty (domain+type is unique for CNAME)", got)
	}
}

// ---------------------------------------------------------------------------
// APIError
// ---------------------------------------------------------------------------

func TestAPIError_Error(t *testing.T) {
	e := &APIError{Status: "error", Message: "Zone does not exist."}
	if got := e.Error(); got != "technitium api error (error): Zone does not exist." {
		t.Errorf("Error() = %q", got)
	}
}

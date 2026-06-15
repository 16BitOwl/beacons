package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// cfOK encodes a successful Cloudflare API response envelope.
func cfOK(result any) string {
	b, _ := json.Marshal(map[string]any{
		"success": true,
		"errors":  []any{},
		"result":  result,
	})
	return string(b)
}

// cfErr encodes a failed Cloudflare API response envelope.
func cfErr(code int, msg string) string {
	return fmt.Sprintf(`{"success":false,"errors":[{"code":%d,"message":%q}],"result":null}`, code, msg)
}

// newTestUpstream builds an Upstream backed by a custom base URL, bypassing
// the zone-validation in New(). Useful for testing Upsert and Delete in isolation.
func newTestUpstream(baseURL, zoneID, zoneName string) *Upstream {
	return &Upstream{
		name:     "test",
		zoneName: zoneName,
		client: &cfClient{
			http:    &http.Client{},
			zoneID:  zoneID,
			baseURL: baseURL,
		},
	}
}

// rec builds a minimal model.Record for use in tests.
func rec(rtype, name, value string, ttl, priority int) model.Record {
	return model.Record{
		BaseRecord: model.BaseRecord{TTL: ttl, Priority: priority},
		Type:       model.RecordType(rtype),
		Name:       name,
		Value:      value,
	}
}

// ---------------------------------------------------------------------------
// fqdn
// ---------------------------------------------------------------------------

func TestFQDN_AppendsZoneSuffix(t *testing.T) {
	u := &Upstream{zoneName: "example.com"}
	if got := u.fqdn("web"); got != "web.example.com" {
		t.Errorf("fqdn(%q) = %q, want web.example.com", "web", got)
	}
}

func TestFQDN_AlreadyFullyQualified_Unchanged(t *testing.T) {
	u := &Upstream{zoneName: "example.com"}
	if got := u.fqdn("web.example.com"); got != "web.example.com" {
		t.Errorf("fqdn(%q) = %q, want unchanged", "web.example.com", got)
	}
}

func TestFQDN_ZoneName_Unchanged(t *testing.T) {
	u := &Upstream{zoneName: "example.com"}
	if got := u.fqdn("example.com"); got != "example.com" {
		t.Errorf("fqdn(%q) = %q, want unchanged", "example.com", got)
	}
}

func TestFQDN_PartialSuffixMatch_AppendsSuffix(t *testing.T) {
	// "notexample.com" ends with "example.com" but is not a subdomain —
	// the suffix check uses "."+zoneName so the zone suffix is appended.
	u := &Upstream{zoneName: "example.com"}
	got := u.fqdn("notexample.com")
	if got != "notexample.com.example.com" {
		t.Errorf("fqdn(%q) = %q, want notexample.com.example.com", "notexample.com", got)
	}
}

// ---------------------------------------------------------------------------
// APIError
// ---------------------------------------------------------------------------

func TestAPIError_HasCode_MatchingCode_ReturnsTrue(t *testing.T) {
	err := &APIError{errors: []apiError{{Code: 81058, Message: "exists"}}}
	if !err.HasCode(81058) {
		t.Error("HasCode(81058) = false, want true")
	}
}

func TestAPIError_HasCode_NonMatchingCode_ReturnsFalse(t *testing.T) {
	err := &APIError{errors: []apiError{{Code: 81058, Message: "exists"}}}
	if err.HasCode(1000) {
		t.Error("HasCode(1000) = true, want false")
	}
}

func TestAPIError_HasCode_EmptyErrors_ReturnsFalse(t *testing.T) {
	if (&APIError{}).HasCode(81058) {
		t.Error("HasCode on empty errors = true, want false")
	}
}

func TestAPIError_Error_IncludesCodeAndMessage(t *testing.T) {
	err := &APIError{errors: []apiError{
		{Code: 1001, Message: "bad token"},
		{Code: 1002, Message: "bad zone"},
	}}
	s := err.Error()
	for _, want := range []string{"1001", "bad token", "1002", "bad zone"} {
		if !strings.Contains(s, want) {
			t.Errorf("Error() = %q, missing %q", s, want)
		}
	}
}

// ---------------------------------------------------------------------------
// New / getZone
// ---------------------------------------------------------------------------

func TestGetZone_Success(t *testing.T) {
	const zoneID = "zone123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, cfOK(map[string]any{"id": zoneID, "name": "example.com"}))
	}))
	defer srv.Close()

	c := &cfClient{http: &http.Client{}, zoneID: zoneID, baseURL: srv.URL}
	z, err := c.getZone(context.Background())
	if err != nil {
		t.Fatalf("getZone: %v", err)
	}
	if z.Name != "example.com" {
		t.Errorf("zone.Name = %q, want example.com", z.Name)
	}
}

func TestGetZone_APIError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, cfErr(7003, "could not route to zone"))
	}))
	defer srv.Close()

	c := &cfClient{http: &http.Client{}, zoneID: "bad", baseURL: srv.URL}
	if _, err := c.getZone(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Upsert
// ---------------------------------------------------------------------------

func TestUpsert_CreatesRecordWhenNoneExist(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, cfOK([]dnsRecord{}))
		case http.MethodPost:
			created = true
			fmt.Fprint(w, cfOK(dnsRecord{ID: "rec1", Type: "A", Name: "web.example.com", Content: "1.2.3.4"}))
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Upsert(context.Background(), rec("A", "web", "1.2.3.4", 300, 0)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !created {
		t.Error("expected POST to create record, but it was not called")
	}
}

func TestUpsert_UpdatesExistingRecord(t *testing.T) {
	updated := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, cfOK([]dnsRecord{{ID: "rec1", Type: "A", Name: "web.example.com", Content: "1.2.3.4"}}))
		case http.MethodPut:
			updated = true
			fmt.Fprint(w, cfOK(dnsRecord{ID: "rec1", Type: "A", Name: "web.example.com", Content: "5.6.7.8"}))
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Upsert(context.Background(), rec("A", "web", "5.6.7.8", 300, 0)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if !updated {
		t.Error("expected PUT to update record, but it was not called")
	}
}

func TestUpsert_AlreadyExistsRace_NotAnError(t *testing.T) {
	// Simulates the race where list returns empty but create returns 81058.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, cfOK([]dnsRecord{}))
		case http.MethodPost:
			fmt.Fprint(w, cfErr(81058, "An identical record already exists."))
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Upsert(context.Background(), rec("A", "web", "1.2.3.4", 300, 0)); err != nil {
		t.Errorf("Upsert returned unexpected error for 81058: %v", err)
	}
}

func TestUpsert_UnsupportedType_ReturnsError(t *testing.T) {
	u := newTestUpstream("http://unused", "zone123", "example.com")
	for _, rtype := range []string{"SRV", "CAA"} {
		if err := u.Upsert(context.Background(), rec(rtype, "svc", "target", 0, 0)); err == nil {
			t.Errorf("Upsert with type %s: expected error, got nil", rtype)
		}
	}
}

func TestUpsert_SetsPriorityOnCreate(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, cfOK([]dnsRecord{}))
		case http.MethodPost:
			json.NewDecoder(r.Body).Decode(&gotBody) //nolint:errcheck
			fmt.Fprint(w, cfOK(dnsRecord{ID: "mx1"}))
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Upsert(context.Background(), rec("MX", "mail", "mail.example.com", 300, 10)); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if p, ok := gotBody["priority"].(float64); !ok || p != 10 {
		t.Errorf("priority in request body = %v, want 10", gotBody["priority"])
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete_DeletesMatchingRecord(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, cfOK([]dnsRecord{{ID: "rec1", Type: "A", Name: "web.example.com", Content: "1.2.3.4"}}))
		case http.MethodDelete:
			deleted = true
			fmt.Fprint(w, cfOK(map[string]string{"id": "rec1"}))
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Delete(context.Background(), rec("A", "web", "1.2.3.4", 0, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Error("expected DELETE call, but it was not made")
	}
}

func TestDelete_RecordNotFound_NoError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, cfOK([]dnsRecord{}))
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Delete(context.Background(), rec("A", "web", "1.2.3.4", 0, 0)); err != nil {
		t.Errorf("Delete on missing record returned unexpected error: %v", err)
	}
}

func TestDelete_DeletesAllMatchingRecords(t *testing.T) {
	deleteCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, cfOK([]dnsRecord{
				{ID: "rec1", Type: "A", Name: "web.example.com", Content: "1.2.3.4"},
				{ID: "rec2", Type: "A", Name: "web.example.com", Content: "1.2.3.4"},
			}))
		case http.MethodDelete:
			deleteCount++
			fmt.Fprint(w, cfOK(map[string]string{"id": "rec1"}))
		default:
			http.Error(w, "unexpected", http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	u := newTestUpstream(srv.URL, "zone123", "example.com")
	if err := u.Delete(context.Background(), rec("A", "web", "1.2.3.4", 0, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleteCount != 2 {
		t.Errorf("DELETE calls = %d, want 2", deleteCount)
	}
}

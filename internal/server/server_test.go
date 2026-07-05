package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/prometheus/client_golang/prometheus"
)

// doRequest routes a GET through the server's mux and returns the recorder.
func doRequest(srv *Server, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	srv.handler.ServeHTTP(w, req)
	return w
}

// failStore is a Store whose List always errors, for exercising error paths.
type failStore struct{}

func (failStore) Upsert(model.Record) error                      { return nil }
func (failStore) Delete(string) error                            { return nil }
func (failStore) DeleteRecord(model.Record) error                { return nil }
func (failStore) List() ([]model.Record, error)                  { return nil, errors.New("boom") }
func (failStore) ListBySourceName(string) ([]model.Record, error) { return nil, nil }

func TestHandleState(t *testing.T) {
	store := registry.NewMemoryStore()
	rec := model.Record{
		ID:         "web",
		SourceID:   "container-1",
		SourceName: "docker",
		Upstream:   "pihole",
		Type:       model.RecordTypeA,
		Name:       "web.example.com",
		Value:      "10.0.0.1",
	}
	if err := store.Upsert(rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	srv := New("", store, prometheus.NewRegistry())

	w := doRequest(srv, "/state")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got stateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Count != 1 {
		t.Errorf("count = %d, want 1", got.Count)
	}
	if len(got.Records) != 1 || got.Records[0] != rec {
		t.Errorf("records = %+v, want [%+v]", got.Records, rec)
	}
}

func TestHandleStateStoreError(t *testing.T) {
	srv := New("", failStore{}, prometheus.NewRegistry())

	w := doRequest(srv, "/state")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleHealth(t *testing.T) {
	store := registry.NewMemoryStore()
	if err := store.Upsert(model.Record{
		ID:       "web",
		Upstream: "pihole",
		Type:     model.RecordTypeA,
		Name:     "web.example.com",
		Value:    "10.0.0.1",
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	srv := New("", store, prometheus.NewRegistry())

	w := doRequest(srv, "/healthz")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got healthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Records != 1 {
		t.Errorf("records = %d, want 1", got.Records)
	}
}

func TestHandleHealthStoreError(t *testing.T) {
	srv := New("", failStore{}, prometheus.NewRegistry())

	w := doRequest(srv, "/healthz")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestHandleMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "beacons_test_counter",
		Help: "test counter",
	}))

	srv := New("", registry.NewMemoryStore(), reg)

	w := doRequest(srv, "/metrics")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), "beacons_test_counter") {
		t.Errorf("metrics output missing registered counter:\n%s", w.Body.String())
	}
}

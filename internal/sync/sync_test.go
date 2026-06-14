package sync

import (
	"context"
	"testing"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
	upstreampkg "github.com/16bitowl/beacons/pkg/upstream"
)

// ---------------------------------------------------------------------------
// Mock store (no mutex — tests are single-goroutine)
// ---------------------------------------------------------------------------

type mockStore struct {
	records map[string]model.Record

	upsertErr error
	deleteErr error
}

func newMockStore() *mockStore {
	return &mockStore{records: make(map[string]model.Record)}
}

func storeKey(r model.Record) string {
	return r.SourceID + "/" + r.ID + "/" + r.Upstream
}

func (m *mockStore) Upsert(r model.Record) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.records[storeKey(r)] = r
	return nil
}

func (m *mockStore) Delete(sourceID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	for k, r := range m.records {
		if r.SourceID == sourceID {
			delete(m.records, k)
		}
	}
	return nil
}

func (m *mockStore) List() ([]model.Record, error) {
	out := make([]model.Record, 0, len(m.records))
	for _, r := range m.records {
		out = append(out, r)
	}
	return out, nil
}

func (m *mockStore) ListBySourceName(name string) ([]model.Record, error) {
	var out []model.Record
	for _, r := range m.records {
		if r.SourceName == name {
			out = append(out, r)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Mock upstream (no mutex — tests are single-goroutine)
// ---------------------------------------------------------------------------

type mockUpstream struct {
	name        string
	upsertCalls []model.Record
	deleteCalls []model.Record
	upsertErr   error
	deleteErr   error
}

func newMockUpstream(name string) *mockUpstream {
	return &mockUpstream{name: name}
}

func (m *mockUpstream) Name() string { return m.name }

func (m *mockUpstream) Upsert(_ context.Context, r model.Record) error {
	if m.upsertErr != nil {
		return m.upsertErr
	}
	m.upsertCalls = append(m.upsertCalls, r)
	return nil
}

func (m *mockUpstream) Delete(_ context.Context, r model.Record) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleteCalls = append(m.deleteCalls, r)
	return nil
}

// Compile-time interface check.
var _ upstreampkg.Upstream = (*mockUpstream)(nil)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeRecord(sourceID, id, upstream string) model.Record {
	return model.Record{
		ID:         id,
		SourceID:   sourceID,
		SourceName: "test-source",
		Upstream:   upstream,
		Type:       model.RecordTypeA,
		Name:       id + ".example.com",
		Value:      "1.2.3.4",
	}
}

func newSyncer(store *mockStore, upstreams map[string]*mockUpstream) *Syncer {
	ups := make(map[string]upstreampkg.Upstream, len(upstreams))
	for k, v := range upstreams {
		ups[k] = v
	}
	return New(Options{
		Store:     store,
		Upstreams: ups,
	})
}

// ---------------------------------------------------------------------------
// upsertRecord
// ---------------------------------------------------------------------------

func TestUpsertRecord_PushesRecordToUpstream(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	s.upsertRecord(context.Background(), makeRecord("src1", "web", "cf"))

	if len(up.upsertCalls) != 1 {
		t.Errorf("upstream.Upsert call count = %d, want 1", len(up.upsertCalls))
	}
}

func TestUpsertRecord_StoresSyncedStatus(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	s.upsertRecord(context.Background(), makeRecord("src1", "web", "cf"))

	records, _ := store.List()
	if len(records) != 1 {
		t.Fatalf("store len = %d, want 1", len(records))
	}
	if records[0].Status != model.RecordStatusSynced {
		t.Errorf("Status = %q, want synced", records[0].Status)
	}
}

func TestUpsertRecord_StoresFailedStatusOnUpstreamError(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	up.upsertErr = &testError{"upstream down"}
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	s.upsertRecord(context.Background(), makeRecord("src1", "web", "cf"))

	records, _ := store.List()
	if len(records) != 1 {
		t.Fatalf("store len = %d, want 1", len(records))
	}
	if records[0].Status != model.RecordStatusFailed {
		t.Errorf("Status = %q, want failed", records[0].Status)
	}
	if records[0].SyncError == "" {
		t.Error("SyncError should be set on failure")
	}
}

func TestUpsertRecord_SyncedAtSetOnSuccess(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	before := time.Now()
	s.upsertRecord(context.Background(), makeRecord("src1", "web", "cf"))
	after := time.Now()

	records, _ := store.List()
	if records[0].SyncedAt.Before(before) || records[0].SyncedAt.After(after) {
		t.Errorf("SyncedAt = %v outside expected range", records[0].SyncedAt)
	}
}

func TestUpsertRecord_UnknownUpstream_NotStored(t *testing.T) {
	store := newMockStore()
	s := newSyncer(store, map[string]*mockUpstream{}) // no upstreams

	s.upsertRecord(context.Background(), makeRecord("src1", "web", "unknown"))

	records, _ := store.List()
	if len(records) != 0 {
		t.Errorf("store len = %d, want 0 (unknown upstream skipped)", len(records))
	}
}

func TestUpsertRecord_SuccessfulRetry_ClearsSyncError(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r := makeRecord("src1", "web", "cf")
	r.Status = model.RecordStatusFailed
	r.SyncError = "previous error"
	_ = store.Upsert(r)

	s.upsertRecord(context.Background(), r)

	records, _ := store.List()
	if records[0].Status != model.RecordStatusSynced {
		t.Errorf("Status = %q, want synced", records[0].Status)
	}
	if records[0].SyncError != "" {
		t.Errorf("SyncError = %q, want empty", records[0].SyncError)
	}
}

// ---------------------------------------------------------------------------
// handleUpsert
// ---------------------------------------------------------------------------

func TestHandleUpsert_UpsertsAllRecords(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	ev := source.Event{
		Type:       source.EventUpsert,
		SourceName: "docker",
		SourceID:   "ctr1",
		Records: []model.Record{
			makeRecord("ctr1", "web", "cf"),
			makeRecord("ctr1", "api", "cf"),
		},
	}
	s.handleUpsert(context.Background(), ev)

	if len(up.upsertCalls) != 2 {
		t.Errorf("upstream.Upsert call count = %d, want 2", len(up.upsertCalls))
	}
	records, _ := store.List()
	if len(records) != 2 {
		t.Errorf("store len = %d, want 2", len(records))
	}
}

// ---------------------------------------------------------------------------
// handleDelete
// ---------------------------------------------------------------------------

func TestHandleDelete_RemovesRecordsForSourceID(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	_ = store.Upsert(makeRecord("ctr1", "web", "cf"))
	_ = store.Upsert(makeRecord("ctr1", "api", "cf"))
	_ = store.Upsert(makeRecord("ctr2", "db", "cf"))

	ev := source.Event{Type: source.EventDelete, SourceName: "docker", SourceID: "ctr1"}
	s.handleDelete(context.Background(), ev)

	if len(up.deleteCalls) != 2 {
		t.Errorf("upstream.Delete call count = %d, want 2", len(up.deleteCalls))
	}
	records, _ := store.List()
	if len(records) != 1 {
		t.Fatalf("store len = %d, want 1 (ctr2 remains)", len(records))
	}
	if records[0].SourceID != "ctr2" {
		t.Errorf("remaining SourceID = %q, want ctr2", records[0].SourceID)
	}
}

func TestHandleDelete_UnknownUpstream_StoreStillCleaned(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r := makeRecord("ctr1", "web", "other")
	_ = store.Upsert(r)

	ev := source.Event{Type: source.EventDelete, SourceName: "docker", SourceID: "ctr1"}
	s.handleDelete(context.Background(), ev)

	if len(up.deleteCalls) != 0 {
		t.Errorf("upstream.Delete should not be called for unknown upstream")
	}
	records, _ := store.List()
	if len(records) != 0 {
		t.Errorf("store len = %d, want 0 (store.Delete should still run)", len(records))
	}
}

// ---------------------------------------------------------------------------
// handleSync
// ---------------------------------------------------------------------------

func TestHandleSync_UpsertsAllSnapshotRecords(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	ev := source.Event{
		Type:       source.EventSync,
		SourceName: "docker",
		Records: []model.Record{
			makeRecord("ctr1", "web", "cf"),
			makeRecord("ctr2", "api", "cf"),
		},
	}
	s.handleSync(context.Background(), ev)

	if len(up.upsertCalls) != 2 {
		t.Errorf("upstream.Upsert call count = %d, want 2", len(up.upsertCalls))
	}
}

func TestHandleSync_RemovesOrphanedRecords(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	// Two records previously stored (e.g. from a prior run).
	r1 := makeRecord("ctr1", "web", "cf")
	r1.SourceName = "docker"
	r2 := makeRecord("ctr2", "api", "cf")
	r2.SourceName = "docker"
	_ = store.Upsert(r1)
	_ = store.Upsert(r2)

	// Snapshot only contains ctr1 — ctr2 is orphaned.
	ev := source.Event{
		Type:       source.EventSync,
		SourceName: "docker",
		Records:    []model.Record{makeRecord("ctr1", "web", "cf")},
	}
	s.handleSync(context.Background(), ev)

	if len(up.deleteCalls) != 1 {
		t.Errorf("upstream.Delete call count = %d, want 1 (orphaned ctr2)", len(up.deleteCalls))
	}
	records, _ := store.List()
	for _, r := range records {
		if r.SourceID == "ctr2" {
			t.Error("orphaned ctr2 should have been removed from store")
		}
	}
}

func TestHandleSync_EmptySnapshot_OrphansAllExisting(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r1 := makeRecord("ctr1", "web", "cf")
	r1.SourceName = "docker"
	r2 := makeRecord("ctr2", "api", "cf")
	r2.SourceName = "docker"
	_ = store.Upsert(r1)
	_ = store.Upsert(r2)

	ev := source.Event{Type: source.EventSync, SourceName: "docker"}
	s.handleSync(context.Background(), ev)

	if len(up.deleteCalls) != 2 {
		t.Errorf("upstream.Delete call count = %d, want 2", len(up.deleteCalls))
	}
	records, _ := store.List()
	if len(records) != 0 {
		t.Errorf("store len = %d, want 0", len(records))
	}
}

func TestHandleSync_OnlyOrphansOwnSourceName(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	dockerRec := makeRecord("ctr1", "web", "cf")
	dockerRec.SourceName = "docker"
	yamlRec := makeRecord("/config/svc.yaml", "api", "cf")
	yamlRec.SourceName = "yaml"
	_ = store.Upsert(dockerRec)
	_ = store.Upsert(yamlRec)

	// Empty docker sync — must not touch the yaml record.
	ev := source.Event{Type: source.EventSync, SourceName: "docker"}
	s.handleSync(context.Background(), ev)

	records, _ := store.List()
	found := false
	for _, r := range records {
		if r.SourceName == "yaml" {
			found = true
		}
	}
	if !found {
		t.Error("yaml record should not be removed by a docker sync event")
	}
}

// ---------------------------------------------------------------------------
// retryFailed
// ---------------------------------------------------------------------------

func TestRetryFailed_RetriesOnlyFailedRecords(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	synced := makeRecord("src1", "web", "cf")
	synced.Status = model.RecordStatusSynced
	failed := makeRecord("src2", "api", "cf")
	failed.Status = model.RecordStatusFailed
	_ = store.Upsert(synced)
	_ = store.Upsert(failed)

	s.retryFailed(context.Background())

	if len(up.upsertCalls) != 1 {
		t.Errorf("upstream.Upsert call count = %d, want 1 (only failed record)", len(up.upsertCalls))
	}
	if up.upsertCalls[0].ID != "api" {
		t.Errorf("retried record ID = %q, want api", up.upsertCalls[0].ID)
	}
}

func TestRetryFailed_NoFailedRecords_NoUpsertCalls(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r := makeRecord("src1", "web", "cf")
	r.Status = model.RecordStatusSynced
	_ = store.Upsert(r)

	s.retryFailed(context.Background())

	if len(up.upsertCalls) != 0 {
		t.Errorf("upstream.Upsert call count = %d, want 0", len(up.upsertCalls))
	}
}

func TestRetryFailed_EmptyStore_NoOp(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	s.retryFailed(context.Background()) // must not panic

	if len(up.upsertCalls) != 0 {
		t.Errorf("upstream.Upsert call count = %d, want 0", len(up.upsertCalls))
	}
}

// ---------------------------------------------------------------------------
// shortID
// ---------------------------------------------------------------------------

func TestShortID_ShortInputPassedThrough(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"", ""},
		{"exactly12chr", "exactly12chr"},
	}
	for _, c := range cases {
		if got := shortID(c.in); got != c.want {
			t.Errorf("shortID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShortID_LongInputTruncatedTo12(t *testing.T) {
	id := "abcdefghijklmnopqrstuvwxyz"
	got := shortID(id)
	if len(got) != 12 {
		t.Errorf("shortID len = %d, want 12", len(got))
	}
	if got != "abcdefghijkl" {
		t.Errorf("shortID = %q, want abcdefghijkl", got)
	}
}

// ---------------------------------------------------------------------------
// testError helper
// ---------------------------------------------------------------------------

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

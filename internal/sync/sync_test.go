package sync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
	upstreampkg "github.com/16bitowl/beacons/pkg/upstream"
)

var errUpstream = errors.New("upstream error")

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
	return model.RecordKey(r)
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

func (m *mockStore) DeleteRecord(r model.Record) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.records, storeKey(r))
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

func TestHandleDelete_UpstreamFailure_MarkedAsPendingDelete(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	up.deleteErr = errUpstream
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	_ = store.Upsert(makeRecord("ctr1", "web", "cf"))

	ev := source.Event{Type: source.EventDelete, SourceName: "docker", SourceID: "ctr1"}
	s.handleDelete(context.Background(), ev)

	records, _ := store.List()
	if len(records) != 1 {
		t.Fatalf("store len = %d, want 1 (record retained after upstream failure)", len(records))
	}
	if records[0].Status != model.RecordStatusPendingDelete {
		t.Errorf("status = %q, want pending_delete", records[0].Status)
	}
	if records[0].Failures != 1 {
		t.Errorf("failures = %d, want 1", records[0].Failures)
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

func TestHandleSync_RenamedRecordWithinSameSource_RemovesOld(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	// A YAML file (single SourceID) previously produced record "web".
	old := makeRecord("/config/svc.yaml", "web", "cf")
	old.SourceName = "yaml"
	_ = store.Upsert(old)

	// The file is reloaded with the record key renamed to "web2" — same file,
	// same upstream, everything else unchanged.
	renamed := makeRecord("/config/svc.yaml", "web2", "cf")
	renamed.SourceName = "yaml"
	ev := source.Event{
		Type:       source.EventSync,
		SourceName: "yaml",
		Records:    []model.Record{renamed},
	}
	s.handleSync(context.Background(), ev)

	// Old record must be deleted from the upstream despite sharing the SourceID.
	if len(up.deleteCalls) != 1 {
		t.Fatalf("upstream.Delete call count = %d, want 1 (renamed-away 'web')", len(up.deleteCalls))
	}
	if up.deleteCalls[0].ID != "web" {
		t.Errorf("deleted record ID = %q, want web", up.deleteCalls[0].ID)
	}

	// Store should hold only the renamed record.
	records, _ := store.List()
	if len(records) != 1 {
		t.Fatalf("store len = %d, want 1", len(records))
	}
	if records[0].ID != "web2" {
		t.Errorf("remaining record ID = %q, want web2", records[0].ID)
	}
}

func TestHandleSync_OrphanUpstreamFailure_MarkedAsPendingDelete(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	up.deleteErr = errUpstream
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	orphan := makeRecord("ctr2", "api", "cf")
	orphan.SourceName = "docker"
	_ = store.Upsert(orphan)

	// Snapshot is empty — ctr2 is orphaned, but upstream delete will fail.
	ev := source.Event{Type: source.EventSync, SourceName: "docker"}
	s.handleSync(context.Background(), ev)

	records, _ := store.List()
	if len(records) != 1 {
		t.Fatalf("store len = %d, want 1 (orphan retained after upstream failure)", len(records))
	}
	if records[0].Status != model.RecordStatusPendingDelete {
		t.Errorf("status = %q, want pending_delete", records[0].Status)
	}
	if records[0].Failures != 1 {
		t.Errorf("failures = %d, want 1", records[0].Failures)
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

func TestRetryFailed_RetriesPendingDeleteRecords(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r := makeRecord("src1", "web", "cf")
	r.Status = model.RecordStatusPendingDelete
	r.Failures = 1
	_ = store.Upsert(r)

	s.retryFailed(context.Background())

	if len(up.deleteCalls) != 1 {
		t.Errorf("upstream.Delete call count = %d, want 1", len(up.deleteCalls))
	}
	if len(up.upsertCalls) != 0 {
		t.Errorf("upstream.Upsert should not be called for pending_delete record")
	}
	// Upstream succeeded — record should be gone from the store.
	records, _ := store.List()
	if len(records) != 0 {
		t.Errorf("store len = %d, want 0 after successful retry", len(records))
	}
}

func TestDeleteRecord_Failures_IncrementsOnEachRetry(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	up.deleteErr = errUpstream
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	_ = store.Upsert(makeRecord("src1", "web", "cf"))

	// First failure.
	records, _ := store.List()
	_ = s.deleteRecord(context.Background(), records[0])
	records, _ = store.List()
	if records[0].Failures != 1 {
		t.Fatalf("failures after 1st attempt = %d, want 1", records[0].Failures)
	}

	// Second failure — counter must increment again.
	s.deleteRecord(context.Background(), records[0])
	records, _ = store.List()
	if records[0].Failures != 2 {
		t.Errorf("failures after 2nd attempt = %d, want 2", records[0].Failures)
	}
}

func TestDeleteRecord_SuccessAfterFailures_ResetsCounter(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r := makeRecord("src1", "web", "cf")
	r.Status = model.RecordStatusPendingDelete
	r.Failures = 3
	_ = store.Upsert(r)

	// Upstream now succeeds.
	s.deleteRecord(context.Background(), r)

	records, _ := store.List()
	if len(records) != 0 {
		t.Errorf("store len = %d, want 0 (record removed after successful delete)", len(records))
	}
}

// ---------------------------------------------------------------------------
// backoffTicks
// ---------------------------------------------------------------------------

func TestBackoffTicks_LowFailureRetryEveryTick(t *testing.T) {
	cases := []struct {
		failures int
		want     uint64
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 8},
		{5, 16},
		{6, 32},
		{7, 32}, // capped
		{100, 32},
	}
	for _, c := range cases {
		if got := backoffTicks(c.failures); got != c.want {
			t.Errorf("backoffTicks(%d) = %d, want %d", c.failures, got, c.want)
		}
	}
}

func TestRetryFailed_BackoffSkipsHighFailureRecords(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	up.deleteErr = errUpstream
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	// failures=3 → backoffTicks=4, so only retried when retryTick%4==0.
	r := makeRecord("src1", "web", "cf")
	r.Status = model.RecordStatusPendingDelete
	r.Failures = 3
	_ = store.Upsert(r)

	// Ticks 1, 2, 3 should all be skipped.
	s.retryFailed(context.Background()) // tick 1
	s.retryFailed(context.Background()) // tick 2
	s.retryFailed(context.Background()) // tick 3
	if len(up.deleteCalls) != 0 {
		t.Errorf("upstream.Delete call count = %d before tick 4, want 0", len(up.deleteCalls))
	}

	// Tick 4 should fire.
	up.deleteErr = nil
	s.retryFailed(context.Background()) // tick 4
	if len(up.deleteCalls) != 1 {
		t.Errorf("upstream.Delete call count = %d at tick 4, want 1", len(up.deleteCalls))
	}
}

// ---------------------------------------------------------------------------
// upsert failure counter
// ---------------------------------------------------------------------------

func TestUpsertRecord_Failures_IncrementsOnEachFailure(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	up.upsertErr = errUpstream
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	_ = store.Upsert(makeRecord("src1", "web", "cf"))

	records, _ := store.List()
	s.upsertRecord(context.Background(), records[0])
	records, _ = store.List()
	if records[0].Failures != 1 {
		t.Fatalf("failures after 1st attempt = %d, want 1", records[0].Failures)
	}

	s.upsertRecord(context.Background(), records[0])
	records, _ = store.List()
	if records[0].Failures != 2 {
		t.Errorf("failures after 2nd attempt = %d, want 2", records[0].Failures)
	}
}

func TestUpsertRecord_Failures_ResetOnSuccess(t *testing.T) {
	store := newMockStore()
	up := newMockUpstream("cf")
	s := newSyncer(store, map[string]*mockUpstream{"cf": up})

	r := makeRecord("src1", "web", "cf")
	r.Status = model.RecordStatusFailed
	r.Failures = 4
	_ = store.Upsert(r)

	s.upsertRecord(context.Background(), r)

	records, _ := store.List()
	if records[0].Failures != 0 {
		t.Errorf("failures after successful upsert = %d, want 0", records[0].Failures)
	}
	if records[0].Status != model.RecordStatusSynced {
		t.Errorf("status = %q, want synced", records[0].Status)
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

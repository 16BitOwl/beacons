package reconcile

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// fakeUpstream is a scripted upstream.Upstream. It records the records passed to
// Upsert/Delete and returns the configured errors.
type fakeUpstream struct {
	name      string
	upsertErr error
	deleteErr error

	mu      sync.Mutex
	upserts []model.Record
	deletes []model.Record
}

func (f *fakeUpstream) Name() string { return f.name }

func (f *fakeUpstream) Upsert(_ context.Context, r model.Record) error {
	f.mu.Lock()
	f.upserts = append(f.upserts, r)
	f.mu.Unlock()
	return f.upsertErr
}

func (f *fakeUpstream) Delete(_ context.Context, r model.Record) error {
	f.mu.Lock()
	f.deletes = append(f.deletes, r)
	f.mu.Unlock()
	return f.deleteErr
}

// panicUpstream panics on every operation, exercising the executor's per-op
// panic recovery.
type panicUpstream struct{ name string }

func (p *panicUpstream) Name() string                               { return p.name }
func (p *panicUpstream) Upsert(context.Context, model.Record) error { panic("adapter boom") }
func (p *panicUpstream) Delete(context.Context, model.Record) error { panic("adapter boom") }

// singleOpPlan builds a plan with one op on the record's own upstream.
func singleOpPlan(kind OpKind, r model.Record) Plan {
	return Plan{Ops: map[string][]Op{r.Upstream: {{Kind: kind, Record: r}}}}
}

// batchingStore wraps a Store and counts Batch calls, so tests can verify
// Executor uses the registry.Batcher extension when the store supports it.
type batchingStore struct {
	registry.Store
	batches int
}

func (b *batchingStore) Batch(fn func() error) error {
	b.batches++
	return fn()
}

func newTestExecutor(store registry.Store, ups map[string]upstream.Upstream, now func() time.Time, backoff func(int) time.Duration) *Executor {
	return NewExecutor(ExecutorOptions{
		Store:     store,
		Upstreams: ups,
		Now:       now,
		Backoff:   backoff,
	})
}

func TestExecutor_CreateWritesSynced(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "cloudflare"}
	e := newTestExecutor(store, map[string]upstream.Upstream{"cloudflare": up}, nil, nil)

	r := rec("docker", "web", "cloudflare")
	e.Apply(context.Background(), singleOpPlan(OpCreate, r), nil)

	if len(up.upserts) != 1 {
		t.Fatalf("upstream upserts: got %d, want 1", len(up.upserts))
	}
	got, _ := store.List()
	if len(got) != 1 {
		t.Fatalf("store: got %d records, want 1", len(got))
	}
	if got[0].Status != model.RecordStatusSynced {
		t.Errorf("status: got %q, want synced", got[0].Status)
	}
}

func TestExecutor_NoopSkipsUpstreamAndStore(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "cloudflare"}
	e := newTestExecutor(store, map[string]upstream.Upstream{"cloudflare": up}, nil, nil)

	r := rec("docker", "web", "cloudflare")
	e.Apply(context.Background(), singleOpPlan(OpNoop, r), nil)

	if len(up.upserts) != 0 || len(up.deletes) != 0 {
		t.Errorf("noop must not call upstream, got %d upserts %d deletes", len(up.upserts), len(up.deletes))
	}
	if got, _ := store.List(); len(got) != 0 {
		t.Errorf("noop must not write store, got %d records", len(got))
	}
}

func TestExecutor_DeleteRemovesFromStore(t *testing.T) {
	store := registry.NewMemoryStore()
	r := rec("yaml", "old", "pihole")
	_ = store.Upsert(r)
	up := &fakeUpstream{name: "pihole"}
	e := newTestExecutor(store, map[string]upstream.Upstream{"pihole": up}, nil, nil)

	e.Apply(context.Background(), singleOpPlan(OpDelete, r), []model.Record{r})

	if len(up.deletes) != 1 {
		t.Fatalf("upstream deletes: got %d, want 1", len(up.deletes))
	}
	if got, _ := store.List(); len(got) != 0 {
		t.Errorf("store should be empty after delete, got %d", len(got))
	}
}

func TestExecutor_UpsertFailureGatesRetry(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "cloudflare", upsertErr: errors.New("boom")}
	clock := time.Unix(1000, 0)
	now := func() time.Time { return clock }
	e := newTestExecutor(store, map[string]upstream.Upstream{"cloudflare": up},
		now, func(int) time.Duration { return time.Hour })

	r := rec("docker", "web", "cloudflare")

	// First pass fails: record stored as failed, retry gated for an hour.
	e.Apply(context.Background(), singleOpPlan(OpCreate, r), nil)
	if len(up.upserts) != 1 {
		t.Fatalf("first pass upserts: got %d, want 1", len(up.upserts))
	}
	got, _ := store.List()
	if len(got) != 1 || got[0].Status != model.RecordStatusFailed || got[0].Failures != 1 {
		t.Fatalf("after failure: got %+v, want failed with 1 failure", got)
	}

	// Second pass within the backoff window is gated: no new upstream call.
	e.Apply(context.Background(), singleOpPlan(OpUpdate, r), got)
	if len(up.upserts) != 1 {
		t.Errorf("gated pass must not call upstream, got %d upserts", len(up.upserts))
	}
}

func TestExecutor_SuccessClearsGateAfterBackoff(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "cloudflare", upsertErr: errors.New("boom")}
	clock := time.Unix(1000, 0)
	now := func() time.Time { return clock }
	e := newTestExecutor(store, map[string]upstream.Upstream{"cloudflare": up},
		now, func(int) time.Duration { return time.Hour })

	r := rec("docker", "web", "cloudflare")
	e.Apply(context.Background(), singleOpPlan(OpCreate, r), nil) // fail, gate set
	got, _ := store.List()

	// Advance past the backoff window and let the upstream recover.
	clock = clock.Add(2 * time.Hour)
	up.upsertErr = nil
	e.Apply(context.Background(), singleOpPlan(OpUpdate, r), got)

	if len(up.upserts) != 2 {
		t.Fatalf("post-backoff pass should retry, got %d upserts", len(up.upserts))
	}
	got, _ = store.List()
	if got[0].Status != model.RecordStatusSynced || got[0].Failures != 0 {
		t.Errorf("after recovery: got status %q failures %d, want synced/0", got[0].Status, got[0].Failures)
	}
}

func TestExecutor_DeleteFailureMarksPendingDelete(t *testing.T) {
	store := registry.NewMemoryStore()
	r := rec("yaml", "old", "pihole")
	_ = store.Upsert(r)
	up := &fakeUpstream{name: "pihole", deleteErr: errors.New("nope")}
	e := newTestExecutor(store, map[string]upstream.Upstream{"pihole": up}, nil, nil)

	e.Apply(context.Background(), singleOpPlan(OpDelete, r), []model.Record{r})

	got, _ := store.List()
	if len(got) != 1 || got[0].Status != model.RecordStatusPendingDelete {
		t.Fatalf("failed delete should mark pending_delete, got %+v", got)
	}
}

func TestExecutor_UnknownUpstream(t *testing.T) {
	store := registry.NewMemoryStore()
	del := rec("yaml", "gone", "removed-upstream")
	_ = store.Upsert(del)
	e := newTestExecutor(store, map[string]upstream.Upstream{}, nil, nil)

	// Delete against an unknown upstream drops the dangling store entry.
	e.Apply(context.Background(), singleOpPlan(OpDelete, del), []model.Record{del})
	if got, _ := store.List(); len(got) != 0 {
		t.Errorf("unknown-upstream delete should drop store entry, got %d", len(got))
	}

	// Create against an unknown upstream is skipped, not persisted.
	create := rec("yaml", "new", "removed-upstream")
	e.Apply(context.Background(), singleOpPlan(OpCreate, create), nil)
	if got, _ := store.List(); len(got) != 0 {
		t.Errorf("unknown-upstream create should be skipped, got %d", len(got))
	}
}

func TestExecutor_RecoversUpstreamPanic(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &panicUpstream{name: "cloudflare"}
	clock := time.Unix(1000, 0)
	e := newTestExecutor(store, map[string]upstream.Upstream{"cloudflare": up},
		func() time.Time { return clock }, func(int) time.Duration { return time.Hour })

	r := rec("docker", "web", "cloudflare")
	// A panicking adapter must not crash the pass; it's recorded as a failure.
	e.Apply(context.Background(), singleOpPlan(OpCreate, r), nil)

	got, _ := store.List()
	if len(got) != 1 || got[0].Status != model.RecordStatusFailed || got[0].Failures != 1 {
		t.Fatalf("recovered panic should mark a failed record, got %+v", got)
	}
}

func TestExecutor_CanceledContextNotCountedAsFailure(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "cloudflare", upsertErr: context.Canceled}
	e := newTestExecutor(store, map[string]upstream.Upstream{"cloudflare": up}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := rec("docker", "web", "cloudflare")
	// Shutdown canceled the op mid-flight: it must not persist a failure or gate.
	e.Apply(ctx, singleOpPlan(OpCreate, r), nil)

	if got, _ := store.List(); len(got) != 0 {
		t.Fatalf("canceled op must not write store, got %+v", got)
	}
}

func TestExecutor_ApplyBatchesStoreWritesWhenSupported(t *testing.T) {
	store := &batchingStore{Store: registry.NewMemoryStore()}
	up := &fakeUpstream{name: "pihole"}
	e := newTestExecutor(store, map[string]upstream.Upstream{"pihole": up}, nil, nil)

	plan := Plan{Ops: map[string][]Op{
		"pihole": {
			{Kind: OpCreate, Record: rec("yaml", "web", "pihole")},
			{Kind: OpCreate, Record: rec("yaml", "api", "pihole")},
		},
	}}
	e.Apply(context.Background(), plan, nil)

	if store.batches != 1 {
		t.Errorf("Batch calls = %d, want 1 (one call regardless of op count)", store.batches)
	}
	if got, _ := store.List(); len(got) != 2 {
		t.Fatalf("expected 2 records written, got %d", len(got))
	}
}

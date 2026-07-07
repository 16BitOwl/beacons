package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// blockingSource is a Snapshotter whose Notify blocks until ctx is canceled, so
// Run can be tested for clean shutdown.
type blockingSource struct{ name string }

func (b *blockingSource) Name() string { return b.name }
func (b *blockingSource) Snapshot(context.Context) ([]model.Record, error) {
	return nil, nil
}
func (b *blockingSource) Notify(ctx context.Context, _ chan<- struct{}) { <-ctx.Done() }

func newTestReconciler(store registry.Store, srcs []source.Snapshotter, ups map[string]upstream.Upstream) *Reconciler {
	return New(Options{Store: store, Sources: srcs, Upstreams: ups})
}

func TestReconcile_CreatesThenDeletesOnCleanDrop(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "pihole"}
	src := &fakeSource{name: "yaml", snap: []model.Record{rec("yaml", "web", "pihole")}}
	r := newTestReconciler(store, []source.Snapshotter{src}, map[string]upstream.Upstream{"pihole": up})

	// First pass creates the record.
	r.reconcile(context.Background())
	if got, _ := store.List(); len(got) != 1 || got[0].Status != model.RecordStatusSynced {
		t.Fatalf("after create: got %+v, want 1 synced record", got)
	}
	if len(up.upserts) != 1 {
		t.Fatalf("upstream upserts: got %d, want 1", len(up.upserts))
	}

	// Source cleanly drops the record -> reconcile deletes it.
	src.snap = nil
	r.reconcile(context.Background())
	if got, _ := store.List(); len(got) != 0 {
		t.Fatalf("after clean drop: store should be empty, got %d", len(got))
	}
	if len(up.deletes) != 1 {
		t.Errorf("upstream deletes: got %d, want 1", len(up.deletes))
	}
}

func TestReconcile_FailedSnapshotKeepsRecords(t *testing.T) {
	store := registry.NewMemoryStore()
	up := &fakeUpstream{name: "pihole"}
	src := &fakeSource{name: "yaml", snap: []model.Record{rec("yaml", "web", "pihole")}}
	r := newTestReconciler(store, []source.Snapshotter{src}, map[string]upstream.Upstream{"pihole": up})

	r.reconcile(context.Background()) // record created

	// Snapshot now errors: the record must survive (no data means no change).
	src.snap, src.err = nil, errors.New("parse error")
	r.reconcile(context.Background())

	if got, _ := store.List(); len(got) != 1 {
		t.Fatalf("failed snapshot must not delete records, got %d", len(got))
	}
	if len(up.deletes) != 0 {
		t.Errorf("failed snapshot must not delete upstream, got %d deletes", len(up.deletes))
	}
}

func TestReconcile_RunShutsDownOnCancel(t *testing.T) {
	store := registry.NewMemoryStore()
	src := &blockingSource{name: "yaml"}
	r := newTestReconciler(store, []source.Snapshotter{src}, map[string]upstream.Upstream{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error on cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not shut down within 2s of cancel")
	}
}

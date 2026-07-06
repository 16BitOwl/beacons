package reconcile

import (
	"context"
	"errors"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
)

// fakeSource is a scripted source.Snapshotter. snap/err are returned by
// Snapshot; calls counts invocations.
type fakeSource struct {
	name  string
	snap  []model.Record
	err   error
	calls int
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Snapshot(context.Context) ([]model.Record, error) {
	f.calls++
	return f.snap, f.err
}

func (f *fakeSource) Notify(context.Context, chan<- struct{}) {}

func TestCollect_MergesCleanSources(t *testing.T) {
	a := &fakeSource{name: "docker", snap: []model.Record{rec("docker", "web", "cloudflare")}}
	b := &fakeSource{name: "yaml", snap: []model.Record{rec("yaml", "api", "pihole")}}
	c := NewCollector(CollectorOptions{Sources: []source.Snapshotter{a, b}})

	desired, snapshotted := c.Collect(context.Background())

	if len(desired) != 2 {
		t.Fatalf("desired: got %d records, want 2", len(desired))
	}
	if !snapshotted["docker"] || !snapshotted["yaml"] {
		t.Errorf("both sources should be snapshotted, got %v", snapshotted)
	}
}

func TestCollect_FailedSourceReusesLastGood(t *testing.T) {
	src := &fakeSource{name: "yaml", snap: []model.Record{rec("yaml", "web", "pihole")}}
	c := NewCollector(CollectorOptions{Sources: []source.Snapshotter{src}})

	// First pass caches the good snapshot.
	if _, snap := c.Collect(context.Background()); !snap["yaml"] {
		t.Fatalf("first pass should snapshot cleanly, got %v", snap)
	}

	// Second pass fails; last-good must still appear in desired, but the source
	// is left out of snapshotted so diff won't delete its records.
	src.snap, src.err = nil, errors.New("parse error")
	desired, snapshotted := c.Collect(context.Background())

	if len(desired) != 1 {
		t.Errorf("desired should reuse last-good (1 record), got %d", len(desired))
	}
	if snapshotted["yaml"] {
		t.Errorf("failed source must be absent from snapshotted, got %v", snapshotted)
	}
}

func TestCollect_FailedSourceNoCacheContributesNothing(t *testing.T) {
	src := &fakeSource{name: "yaml", err: errors.New("boom")}
	c := NewCollector(CollectorOptions{Sources: []source.Snapshotter{src}})

	desired, snapshotted := c.Collect(context.Background())

	if len(desired) != 0 {
		t.Errorf("no cache -> no desired records, got %d", len(desired))
	}
	if len(snapshotted) != 0 {
		t.Errorf("failed source must not be snapshotted, got %v", snapshotted)
	}
}

func TestCollect_CleanEmptyReadIsSnapshotted(t *testing.T) {
	// A clean read yielding no records is a valid snapshot: the source is marked
	// snapshotted so diff can delete its orphans.
	src := &fakeSource{name: "docker", snap: nil}
	c := NewCollector(CollectorOptions{Sources: []source.Snapshotter{src}})

	desired, snapshotted := c.Collect(context.Background())

	if len(desired) != 0 {
		t.Errorf("empty read -> no desired records, got %d", len(desired))
	}
	if !snapshotted["docker"] {
		t.Errorf("clean empty read must be snapshotted, got %v", snapshotted)
	}
}

func TestCollect_UpdatesCacheOnSubsequentCleanRead(t *testing.T) {
	src := &fakeSource{name: "docker", snap: []model.Record{rec("docker", "web", "cloudflare")}}
	c := NewCollector(CollectorOptions{Sources: []source.Snapshotter{src}})

	c.Collect(context.Background()) // cache first snapshot

	src.snap = []model.Record{
		rec("docker", "web", "cloudflare"),
		rec("docker", "api", "cloudflare"),
	}
	desired, _ := c.Collect(context.Background())

	if len(desired) != 2 {
		t.Errorf("cache should update to latest clean read (2 records), got %d", len(desired))
	}
}

func TestCollect_FeedsDiff_FailedSourceNoDelete(t *testing.T) {
	// End-to-end with diff: a source's records are recorded, then the source
	// fails. Collect reuses last-good and omits the source from snapshotted, so
	// diff produces a noop rather than a delete.
	r := rec("yaml", "web", "pihole")
	src := &fakeSource{name: "yaml", snap: []model.Record{r}}
	c := NewCollector(CollectorOptions{Sources: []source.Snapshotter{src}})

	c.Collect(context.Background()) // cache last-good
	src.snap, src.err = nil, errors.New("read failed")
	desired, snapshotted := c.Collect(context.Background())

	got := kindByKey(t, diff(desired, []model.Record{r}, snapshotted))
	if got[model.RecordKey(r)] != OpNoop {
		t.Errorf("failed source record: got %v, want noop", got[model.RecordKey(r)])
	}
}

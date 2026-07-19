package reconcile

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// fakeListerUpstream is an upstream.Upstream that also implements
// upstream.Lister, for exercising UpstreamCollector in isolation.
type fakeListerUpstream struct {
	name    string
	records []model.Record
	err     error
	calls   int
}

func (f *fakeListerUpstream) Name() string { return f.name }

func (f *fakeListerUpstream) Upsert(context.Context, model.Record) error { return nil }

func (f *fakeListerUpstream) Delete(context.Context, model.Record) error { return nil }

func (f *fakeListerUpstream) List(context.Context) ([]model.Record, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.records, nil
}

func TestUpstreamCollector_SkipsNonLister(t *testing.T) {
	// fakeUpstream (executor_test.go) implements only Upsert/Delete/Name.
	u := &fakeUpstream{name: "pihole"}
	c := NewUpstreamCollector(UpstreamCollectorOptions{
		Upstreams:      map[string]upstream.Upstream{"pihole": u},
		VerifyInterval: map[string]time.Duration{"pihole": time.Minute},
	})

	actual, fetched := c.Collect(context.Background(), time.Now())
	if fetched["pihole"] {
		t.Error("fetched[pihole] = true, want false: upstream is not a Lister")
	}
	if len(actual["pihole"]) != 0 {
		t.Errorf("actual[pihole] = %v, want empty", actual["pihole"])
	}
}

func TestUpstreamCollector_SkipsWhenIntervalZero(t *testing.T) {
	u := &fakeListerUpstream{name: "cf", records: []model.Record{{}}}
	// No VerifyInterval entry for "cf" -> disabled by default.
	c := NewUpstreamCollector(UpstreamCollectorOptions{
		Upstreams: map[string]upstream.Upstream{"cf": u},
	})

	_, fetched := c.Collect(context.Background(), time.Now())
	if fetched["cf"] {
		t.Error("fetched[cf] = true, want false: verification disabled (no interval configured)")
	}
	if u.calls != 0 {
		t.Errorf("List called %d times, want 0", u.calls)
	}
}

func TestUpstreamCollector_FetchesWhenDue(t *testing.T) {
	want := []model.Record{{ID: "a"}, {ID: "b"}}
	u := &fakeListerUpstream{name: "cf", records: want}
	c := NewUpstreamCollector(UpstreamCollectorOptions{
		Upstreams:      map[string]upstream.Upstream{"cf": u},
		VerifyInterval: map[string]time.Duration{"cf": time.Minute},
	})

	actual, fetched := c.Collect(context.Background(), time.Now())
	if !fetched["cf"] {
		t.Fatal("fetched[cf] = false, want true")
	}
	if len(actual["cf"]) != 2 {
		t.Errorf("actual[cf] = %v, want 2 records", actual["cf"])
	}
}

func TestUpstreamCollector_ThrottledUntilIntervalElapses(t *testing.T) {
	u := &fakeListerUpstream{name: "cf"}
	c := NewUpstreamCollector(UpstreamCollectorOptions{
		Upstreams:      map[string]upstream.Upstream{"cf": u},
		VerifyInterval: map[string]time.Duration{"cf": time.Minute},
	})

	now := time.Now()
	if _, fetched := c.Collect(context.Background(), now); !fetched["cf"] {
		t.Fatal("first pass: fetched[cf] = false, want true")
	}
	if _, fetched := c.Collect(context.Background(), now.Add(30*time.Second)); fetched["cf"] {
		t.Error("pass before interval elapsed: fetched[cf] = true, want false")
	}
	if _, fetched := c.Collect(context.Background(), now.Add(61*time.Second)); !fetched["cf"] {
		t.Error("pass after interval elapsed: fetched[cf] = false, want true")
	}
	if u.calls != 2 {
		t.Errorf("List called %d times, want 2 (throttled pass skipped)", u.calls)
	}
}

func TestUpstreamCollector_ListErrorOmitsWithoutCaching(t *testing.T) {
	u := &fakeListerUpstream{name: "cf", err: errors.New("rate limited")}
	c := NewUpstreamCollector(UpstreamCollectorOptions{
		Upstreams:      map[string]upstream.Upstream{"cf": u},
		VerifyInterval: map[string]time.Duration{"cf": time.Minute},
	})

	now := time.Now()
	actual, fetched := c.Collect(context.Background(), now)
	if fetched["cf"] {
		t.Error("fetched[cf] = true, want false on List error")
	}
	if len(actual["cf"]) != 0 {
		t.Errorf("actual[cf] = %v, want empty on List error", actual["cf"])
	}

	// A failed call must not set the throttle gate: the very next pass retries
	// immediately rather than waiting out the interval, unlike a success.
	u.err = nil
	if _, fetched := c.Collect(context.Background(), now.Add(time.Second)); !fetched["cf"] {
		t.Error("pass right after a failed List: fetched[cf] = false, want true (no gate set on error)")
	}
}

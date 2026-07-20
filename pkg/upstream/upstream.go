package upstream

import (
	"context"
	"log/slog"

	"github.com/16bitowl/beacons/internal/model"
)

// Upstream is the interface all upstream DNS adapters must implement.
type Upstream interface {
	// Name returns the unique name of this upstream instance.
	Name() string

	// Upsert creates or updates a DNS record.
	Upsert(ctx context.Context, r model.Record) error

	// Delete removes a DNS record.
	Delete(ctx context.Context, r model.Record) error
}

// Lister is implemented by upstream adapters that can read back their current
// record set. Reconcile uses it to detect drift between what the store
// believes is synced and what the upstream actually holds. An upstream that
// doesn't implement Lister is simply never verified — existing two-way
// reconcile behavior for it is unchanged.
type Lister interface {
	// List returns every record the upstream currently holds. Returned records
	// carry Type/Name/Value/Upstream (and TTL/Priority where modeled); SourceID
	// and ID are left zero since the upstream has no concept of them — reconcile
	// matches these against desired state by content, not by RecordKey.
	List(ctx context.Context) ([]model.Record, error)
}

// DriftComparer lets an upstream adapter override which applied fields matter
// for drift detection, for adapters that cannot round-trip every field the
// two-way diff considers. Upstreams that don't implement it are compared on
// every applied field (Type/Name/Value/TTL/Priority/Comment).
type DriftComparer interface {
	// DriftEqual reports whether got (a record from List) should be considered
	// in sync with want (desired), for drift detection. Both records carry
	// only the fields List() populates; RecordKey identity fields (ID,
	// SourceID, Status, ...) are irrelevant here and must not be compared.
	DriftEqual(want, got model.Record) bool
}

// Disabled is an upstream that failed to initialize. Every operation returns
// the original init error so records are marked failed in the store rather than
// silently dropped. The retry loop will keep attempting them; once the
// credentials are fixed and the process is restarted the upstream initializes
// normally and the retry loop drains any pending records.
type Disabled struct {
	name string
	err  error
}

func NewDisabled(name string, err error) *Disabled {
	return &Disabled{name: name, err: err}
}

func (d *Disabled) Name() string { return d.name }

func (d *Disabled) Upsert(_ context.Context, _ model.Record) error { return d.err }

func (d *Disabled) Delete(_ context.Context, _ model.Record) error { return d.err }

// DryRun wraps an Upstream and logs operations instead of applying them.
type DryRun struct {
	wrapped Upstream
}

func NewDryRun(u Upstream) *DryRun { return &DryRun{wrapped: u} }

func (d *DryRun) Name() string { return d.wrapped.Name() }

func (d *DryRun) Upsert(_ context.Context, r model.Record) error {
	slog.Info("[dry-run] would upsert record",
		"upstream", d.wrapped.Name(),
		"id", r.ID,
		"type", r.Type,
		"name", r.Name,
		"value", r.Value,
		"ttl", r.TTL,
	)
	return nil
}

func (d *DryRun) Delete(_ context.Context, r model.Record) error {
	slog.Info("[dry-run] would delete record",
		"upstream", d.wrapped.Name(),
		"id", r.ID,
		"type", r.Type,
		"name", r.Name,
	)
	return nil
}

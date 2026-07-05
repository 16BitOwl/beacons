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

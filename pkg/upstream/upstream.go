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

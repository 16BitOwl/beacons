package source

import (
	"context"

	"github.com/16bitowl/beacons/internal/model"
)

// EventType signals the kind of change from a source adapter.
type EventType string

const (
	// EventUpsert adds or updates records for a specific source item (e.g. one container).
	EventUpsert EventType = "upsert"

	// EventDelete removes all records for a specific source item.
	EventDelete EventType = "delete"

	// EventSync carries the complete current record set for an entire source adapter.
	// The syncer removes any stored records attributed to this source that are absent
	// from the event, then upserts everything present. Use this for startup snapshots
	// and full-state refreshes.
	EventSync EventType = "sync"
)

// Event carries a batch of records and the type of change.
type Event struct {
	Type EventType

	// SourceName is the name of the source adapter instance that emitted this event.
	SourceName string

	// SourceID identifies the specific source item (container ID, file path, etc.).
	// Used by EventUpsert and EventDelete; empty for EventSync.
	SourceID string

	Records []model.Record
}

// Source is the interface all source adapters must implement.
type Source interface {
	// Name returns the unique name of this source instance.
	Name() string

	// Run starts the source adapter. It emits events on ch and blocks until ctx
	// is canceled. Implementations should handle both polling and event-driven
	// modes as configured.
	Run(ctx context.Context, ch chan<- Event) error
}

// Snapshotter is the reconciler-facing source contract: a best-effort provider
// of the records a source currently wants, plus a change signal. Adapters
// implement it alongside Run.
type Snapshotter interface {
	// Name returns the unique name of this source instance.
	Name() string

	// Snapshot returns the complete current record set for this source. It must
	// return a non-nil error rather than a partial or empty set on any
	// read/parse failure, so the reconciler can retain the last good snapshot. A
	// clean read that legitimately yields no records returns an empty slice and a
	// nil error.
	Snapshot(ctx context.Context) ([]model.Record, error)

	// Notify sends a signal on ch whenever this source's records may have changed,
	// prompting the reconciler to re-snapshot. Implementations coalesce their own
	// event bursts. Notify blocks until ctx is canceled and does not close ch —
	// the reconciler owns the channel and fans in across sources.
	Notify(ctx context.Context, ch chan<- struct{})
}

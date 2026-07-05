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

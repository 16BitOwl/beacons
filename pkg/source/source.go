package source

import (
	"context"

	"github.com/16bitowl/beacons/internal/model"
)

// Event signals a change from a source adapter.
type EventType string

const (
	EventUpsert EventType = "upsert"
	EventDelete EventType = "delete"
)

// Event carries a batch of records and the type of change.
type Event struct {
	Type     EventType
	SourceID string
	Records  []model.Record
}

// Source is the interface all source adapters must implement.
type Source interface {
	// Name returns the unique name of this source instance.
	Name() string

	// Run starts the source adapter. It emits events on ch and blocks until ctx
	// is cancelled. Implementations should handle both polling and event-driven
	// modes as configured.
	Run(ctx context.Context, ch chan<- Event) error
}

// Package registry defines the Store interface for DNS record persistence and
// provides built-in implementations (MemoryStore, FileStore).
//
// To add a new backend (e.g. SQLite, Postgres), create a new file in this
// package that implements the Store interface.
package registry

import "github.com/16bitowl/beacons/internal/model"

// Store is the interface for DNS record persistence.
// Implementations can be in-memory, flat-file, SQLite, etc.
type Store interface {
	// Upsert adds or updates a record. The record is keyed by
	// (sourceID, recordID, upstream); all other fields including
	// sync status are overwritten.
	Upsert(r model.Record) error

	// Delete removes all records for a given sourceID.
	Delete(sourceID string) error

	// DeleteRecord removes a single record identified by (sourceID, recordID, upstream).
	// It is a no-op if the record does not exist.
	DeleteRecord(r model.Record) error

	// List returns all currently stored records.
	List() ([]model.Record, error)

	// ListBySourceName returns all records produced by the named source adapter.
	ListBySourceName(sourceName string) ([]model.Record, error)
}

// Batcher is an optional Store extension for backends whose per-write cost
// scales with record count (e.g. FileStore). Callers making several mutations
// as one unit feature-detect it and wrap them in Batch; stores without it are
// treated as cheap per write (e.g. MemoryStore).
type Batcher interface {
	// Batch runs fn with the per-write durability step deferred, then performs
	// it once. Not atomic: whatever fn wrote to in-memory state is persisted
	// regardless of the error returned.
	Batch(fn func() error) error
}

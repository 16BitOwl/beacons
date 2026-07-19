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
// scales with total record count (e.g. FileStore rewrites the whole file on
// every call). A caller making several mutations as one logical unit — a
// reconcile pass — should feature-detect Batcher and wrap them in Batch, so
// the backend can defer its expensive part to a single call instead of one
// per record. A store that doesn't implement Batcher is assumed cheap enough
// per write that batching wouldn't help (e.g. MemoryStore).
type Batcher interface {
	// Batch runs fn with the store's per-write durability step deferred, then
	// performs that step once. A logical pass is not one atomic transaction:
	// whatever fn wrote to in-memory state before returning (error or not) is
	// still persisted by the deferred step.
	Batch(fn func() error) error
}

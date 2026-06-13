package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// Syncer orchestrates events from sources into the registry and pushes to upstreams.
type Syncer struct {
	store         registry.Store
	upstreams     map[string]upstream.Upstream
	retryInterval time.Duration
}

// New creates a Syncer. retryInterval controls how often failed records are
// re-attempted; pass 0 to disable automatic retries.
func New(store registry.Store, upstreams map[string]upstream.Upstream, retryInterval time.Duration) *Syncer {
	return &Syncer{store: store, upstreams: upstreams, retryInterval: retryInterval}
}

// Run starts all sources and processes their events until ctx is cancelled.
func (s *Syncer) Run(ctx context.Context, sources []source.Source) error {
	ch := make(chan source.Event, 64)

	for _, src := range sources {
		slog.Info("starting source", "source", src.Name())
		go func(src source.Source) {
			if err := src.Run(ctx, ch); err != nil && ctx.Err() == nil {
				slog.Error("source exited",
					"source", src.Name(),
					"err", err)
			} else {
				slog.Info("source stopped",
					"source", src.Name())
			}
		}(src)
	}

	var retryC <-chan time.Time
	if s.retryInterval > 0 {
		t := time.NewTicker(s.retryInterval)
		defer t.Stop()
		retryC = t.C
		slog.Info("failed-record retry enabled", "interval", s.retryInterval)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("syncer shutting down")
			return nil
		case ev := <-ch:
			s.handle(ctx, ev)
		case <-retryC:
			s.retryFailed(ctx)
		}
	}
}

func (s *Syncer) handle(ctx context.Context, ev source.Event) {
	switch ev.Type {
	case source.EventSync:
		s.handleSync(ctx, ev)
	case source.EventUpsert:
		s.handleUpsert(ctx, ev)
	case source.EventDelete:
		s.handleDelete(ctx, ev)
	}
}

// handleSync processes a full-state snapshot from a source. It upserts all
// records present in the snapshot and removes any records previously attributed
// to this source that are absent — cleaning up both the store and the upstream.
// This is the mechanism that handles containers (or files) removed while Beacons
// was offline.
func (s *Syncer) handleSync(ctx context.Context, ev source.Event) {
	slog.Debug("processing sync event",
		"source", ev.SourceName,
		"records", len(ev.Records))

	// Fetch all records currently in the store for this source adapter.
	existing, err := s.store.ListBySourceName(ev.SourceName)
	if err != nil {
		slog.Error("store list failed during sync",
			"source", ev.SourceName,
			"err", err)
		return
	}

	// Build the set of sourceIDs present in the incoming snapshot.
	activeSourceIDs := make(map[string]struct{}, len(ev.Records))
	for _, r := range ev.Records {
		activeSourceIDs[r.SourceID] = struct{}{}
	}

	// Find orphaned sourceIDs: present in the store but absent from the snapshot.
	// These represent source items (containers, files) that disappeared while
	// Beacons was not running.
	orphanedSourceIDs := make(map[string]struct{})
	for _, r := range existing {
		if _, ok := activeSourceIDs[r.SourceID]; !ok {
			orphanedSourceIDs[r.SourceID] = struct{}{}
		}
	}

	// Delete orphaned records from upstreams, then remove them from the store.
	if len(orphanedSourceIDs) > 0 {
		slog.Info("sync: removing orphaned records",
			"source", ev.SourceName,
			"orphaned_source_count", len(orphanedSourceIDs))

		for _, r := range existing {
			if _, ok := orphanedSourceIDs[r.SourceID]; !ok {
				continue
			}
			u, ok := s.upstreams[r.Upstream]
			if !ok {
				slog.Warn("sync: unknown upstream for orphaned record, skipping",
					"upstream", r.Upstream,
					"record", r.ID)
				continue
			}
			slog.Info("sync: deleting orphaned record",
				"source", ev.SourceName,
				"source_id", shortID(r.SourceID),
				"record", r.ID,
				"name", r.Name)
			if err := u.Delete(ctx, r); err != nil {
				slog.Error("sync: upstream delete failed for orphan",
					"record", r.ID,
					"upstream", r.Upstream,
					"err", err)
			}
		}
		for sid := range orphanedSourceIDs {
			if err := s.store.Delete(sid); err != nil {
				slog.Error("sync: store delete failed for orphan",
					"source_id", sid,
					"err", err)
			}
		}
	}

	// Upsert all records from the snapshot.
	for _, r := range ev.Records {
		s.upsertRecord(ctx, r)
	}
}

// handleUpsert processes incremental add/update events (e.g. a container starting).
func (s *Syncer) handleUpsert(ctx context.Context, ev source.Event) {
	slog.Debug("processing upsert event",
		"source", ev.SourceName,
		"source_id", ev.SourceID,
		"records", len(ev.Records))
	for _, r := range ev.Records {
		s.upsertRecord(ctx, r)
	}
}

// handleDelete processes removal events (e.g. a container stopping).
func (s *Syncer) handleDelete(ctx context.Context, ev source.Event) {
	slog.Debug("processing delete event",
		"source", ev.SourceName,
		"source_id", ev.SourceID)

	records, err := s.store.List()
	if err != nil {
		slog.Error("store list failed during delete", "err", err)
		return
	}

	deleted := 0
	for _, r := range records {
		if r.SourceID != ev.SourceID {
			continue
		}
		u, ok := s.upstreams[r.Upstream]
		if !ok {
			continue
		}
		slog.Info("deleting record",
			"upstream", r.Upstream,
			"record", r.ID,
			"type", r.Type,
			"name", r.Name)
		if err := u.Delete(ctx, r); err != nil {
			slog.Error("upstream delete failed",
				"upstream", r.Upstream,
				"record", r.ID,
				"err", err)
		} else {
			deleted++
		}
	}
	if err := s.store.Delete(ev.SourceID); err != nil {
		slog.Error("store delete failed",
			"source_id", ev.SourceID,
			"err", err)
	} else {
		slog.Info("source records deleted",
			"source", ev.SourceName,
			"source_id", ev.SourceID,
			"count", deleted)
	}
}

// upsertRecord pushes a single record to its upstream and writes the result
// (including sync status) back to the store.
func (s *Syncer) upsertRecord(ctx context.Context, r model.Record) {
	u, ok := s.upstreams[r.Upstream]
	if !ok {
		slog.Warn("unknown upstream, skipping",
			"upstream", r.Upstream,
			"record", r.ID)
		return
	}

	slog.Info("upserting record",
		"upstream", r.Upstream,
		"record", r.ID,
		"type", r.Type,
		"name", r.Name,
		"value", r.Value)

	if err := u.Upsert(ctx, r); err != nil {
		slog.Error("upstream upsert failed",
			"upstream", r.Upstream,
			"record", r.ID,
			"err", err)
		r.Status = model.RecordStatusFailed
		r.SyncError = err.Error()
	} else {
		slog.Info("record upserted successfully",
			"upstream", r.Upstream,
			"record", r.ID,
			"name", r.Name)
		r.Status = model.RecordStatusSynced
		r.SyncedAt = time.Now()
		r.SyncError = ""
	}

	if err := s.store.Upsert(r); err != nil {
		slog.Error("store upsert failed",
			"record", r.ID,
			"err", err)
	}
}

// retryFailed re-attempts every record in the store that is currently marked
// as failed. It is called on a ticker and runs entirely off the store — no
// source events are needed for retries to fire.
func (s *Syncer) retryFailed(ctx context.Context) {
	records, err := s.store.List()
	if err != nil {
		slog.Error("retry: store list failed", "err", err)
		return
	}

	var failed []model.Record
	for _, r := range records {
		if r.Status == model.RecordStatusFailed {
			failed = append(failed, r)
		}
	}
	if len(failed) == 0 {
		return
	}

	slog.Info("retrying failed records", "count", len(failed))
	for _, r := range failed {
		s.upsertRecord(ctx, r)
	}
}

// shortID returns up to 12 characters of an ID for log readability.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

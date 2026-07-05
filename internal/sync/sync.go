package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/16bitowl/beacons/internal/metrics"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// Options configures a Syncer.
type Options struct {
	Store         registry.Store
	Upstreams     map[string]upstream.Upstream
	RetryInterval time.Duration
	// Metrics is optional; pass nil to disable metrics recording.
	Metrics *metrics.Metrics
}

// Syncer orchestrates events from sources into the registry and pushes to upstreams.
type Syncer struct {
	store         registry.Store
	upstreams     map[string]upstream.Upstream
	retryInterval time.Duration
	metrics       *metrics.Metrics
	retryTick     uint64
}

// New creates a Syncer. RetryInterval controls how often failed records are
// re-attempted; pass 0 to disable automatic retries.
func New(opts Options) *Syncer {
	return &Syncer{
		store:         opts.Store,
		upstreams:     opts.Upstreams,
		retryInterval: opts.RetryInterval,
		metrics:       opts.Metrics,
	}
}

// Run starts all sources and processes their events until ctx is canceled.
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
// Orphan detection is keyed on the full record key (sourceID, recordID,
// upstream) — the same key the store uses.
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

	// Build the set of record keys present in the incoming snapshot.
	activeKeys := make(map[string]struct{}, len(ev.Records))
	for _, r := range ev.Records {
		activeKeys[model.RecordKey(r)] = struct{}{}
	}

	// Find orphaned records: present in the store but absent from the snapshot.
	var orphaned []model.Record
	for _, r := range existing {
		if _, ok := activeKeys[model.RecordKey(r)]; !ok {
			orphaned = append(orphaned, r)
		}
	}

	// Delete orphaned records from upstreams and remove successful ones from the
	// store. Records whose upstream delete fails are marked pending_delete so
	// the retry loop re-attempts them independently of the next sync cycle.
	// Deletion runs before the upsert loop so a renamed record's old entry is
	// removed before its replacement is pushed.
	if len(orphaned) > 0 {
		slog.Info("sync: removing orphaned records",
			"source", ev.SourceName,
			"orphaned_count", len(orphaned))

		for _, r := range orphaned {
			slog.Info("sync: deleting orphaned record",
				"source", ev.SourceName,
				"source_id", shortID(r.SourceID),
				"record", r.ID,
				"name", r.Name)
			_ = s.deleteRecord(ctx, r) // failure already handled internally (marks pending_delete)
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
		if err := s.deleteRecord(ctx, r); err == nil {
			deleted++
		}
	}
	slog.Info("source records deleted",
		"source", ev.SourceName,
		"source_id", shortID(ev.SourceID),
		"count", deleted)
}

// deleteRecord removes a single record from its upstream and the store.
//
// On success the record is removed from the store and nil is returned.
// On upstream failure the record is written back to the store with status
// pending_delete and an incremented Failures counter, and the error is
// returned. The retry loop will re-attempt pending_delete records on its next
// eligible tick (subject to exponential back-off via backoffTicks).
func (s *Syncer) deleteRecord(ctx context.Context, r model.Record) error {
	u, ok := s.upstreams[r.Upstream]
	if !ok {
		// Unknown upstream — nothing to clean up remotely; drop the dangling entry.
		if err := s.store.DeleteRecord(r); err != nil {
			slog.Error("store delete failed for unknown-upstream record",
				"record", r.ID,
				"err", err)
		}
		return nil
	}

	slog.Info("deleting record",
		"upstream", r.Upstream,
		"record", r.ID,
		"type", r.Type,
		"name", r.Name)

	start := time.Now()
	err := u.Delete(ctx, r)
	if s.metrics != nil {
		result := "success"
		if err != nil {
			result = "failure"
		}
		s.metrics.RecordSync(r.Upstream, "delete", result, time.Since(start))
	}
	if err != nil {
		r.Failures++
		r.Status = model.RecordStatusPendingDelete
		r.SyncError = err.Error()
		slog.Error("upstream delete failed, marked as pending_delete",
			"upstream", r.Upstream,
			"record", r.ID,
			"failures", r.Failures,
			"err", err)
		if storeErr := s.store.Upsert(r); storeErr != nil {
			slog.Error("store upsert failed after delete failure",
				"record", r.ID,
				"err", storeErr)
		}
		return err
	}

	slog.Info("record deleted successfully",
		"upstream", r.Upstream,
		"record", r.ID,
		"name", r.Name)
	if storeErr := s.store.DeleteRecord(r); storeErr != nil {
		slog.Error("store delete failed",
			"record", r.ID,
			"err", storeErr)
	}
	return nil
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

	start := time.Now()
	err := u.Upsert(ctx, r)
	if s.metrics != nil {
		result := "success"
		if err != nil {
			result = "failure"
		}
		s.metrics.RecordSync(r.Upstream, "upsert", result, time.Since(start))
	}
	if err != nil {
		r.Failures++
		r.Status = model.RecordStatusFailed
		r.SyncError = err.Error()
		slog.Error("upstream upsert failed",
			"upstream", r.Upstream,
			"record", r.ID,
			"failures", r.Failures,
			"err", err)
	} else {
		r.Failures = 0
		r.Status = model.RecordStatusSynced
		r.SyncedAt = time.Now()
		r.SyncError = ""
		slog.Info("record upserted successfully",
			"upstream", r.Upstream,
			"record", r.ID,
			"name", r.Name)
	}

	if err := s.store.Upsert(r); err != nil {
		slog.Error("store upsert failed",
			"record", r.ID,
			"err", err)
	}
}

// retryFailed re-attempts records that are in a non-terminal error state.
// It is called on a ticker and runs entirely off the store — no source events
// are needed for retries to fire.
//
//   - failed records are re-pushed to their upstream via upsertRecord.
//   - pending_delete records are re-deleted from their upstream via deleteRecord.
//
// Records are subject to exponential back-off: a record with N consecutive
// failures is only retried every backoffTicks(N) ticks, so transient errors
// (or permanently broken credentials) do not hammer the upstream API.
func (s *Syncer) retryFailed(ctx context.Context) {
	s.retryTick++

	records, err := s.store.List()
	if err != nil {
		slog.Error("retry: store list failed", "err", err)
		return
	}

	var toUpsert, toDelete []model.Record
	for _, r := range records {
		switch r.Status {
		case model.RecordStatusFailed:
			if s.retryTick%backoffTicks(r.Failures) == 0 {
				toUpsert = append(toUpsert, r)
			}
		case model.RecordStatusPendingDelete:
			if s.retryTick%backoffTicks(r.Failures) == 0 {
				toDelete = append(toDelete, r)
			}
		}
	}
	if len(toUpsert) == 0 && len(toDelete) == 0 {
		return
	}

	if len(toUpsert) > 0 {
		slog.Info("retrying failed upsert records", "count", len(toUpsert))
		for _, r := range toUpsert {
			s.upsertRecord(ctx, r)
		}
	}
	if len(toDelete) > 0 {
		slog.Info("retrying pending delete records", "count", len(toDelete))
		for _, r := range toDelete {
			_ = s.deleteRecord(ctx, r) // failure already handled internally (marks pending_delete)
		}
	}
}

// shortID returns up to 12 characters of an ID for log readability.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

// backoffTicks returns how many retry ticks must elapse between attempts for a
// record with the given consecutive failure count. The interval doubles with
// each failure, capped at 2^5 = 32 ticks.
//
//	failures 0–1 → every tick (1)
//	failures 2   → every 2 ticks
//	failures 3   → every 4 ticks
//	failures 6+  → every 32 ticks (cap)
func backoffTicks(failures int) uint64 {
	if failures <= 1 {
		return 1
	}
	shift := failures - 1
	if shift > 5 {
		shift = 5
	}
	return 1 << uint(shift)
}

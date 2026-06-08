package sync

import (
	"context"
	"log/slog"

	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/pkg/source"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// Syncer orchestrates events from sources into the registry and pushes to upstreams.
type Syncer struct {
	store     registry.Store
	upstreams map[string]upstream.Upstream
}

func New(store registry.Store, upstreams map[string]upstream.Upstream) *Syncer {
	return &Syncer{store: store, upstreams: upstreams}
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

	for {
		select {
		case <-ctx.Done():
			slog.Info("syncer shutting down")
			return nil
		case ev := <-ch:
			s.handle(ctx, ev)
		}
	}
}

func (s *Syncer) handle(ctx context.Context, ev source.Event) {
	switch ev.Type {
	case source.EventUpsert:
		slog.Debug("processing upsert event", "source", ev.SourceID, "records", len(ev.Records))
		for _, r := range ev.Records {
			if err := s.store.Upsert(r); err != nil {
				slog.Error("registry upsert failed",
					"record", r.ID,
					"err", err)
				continue
			}
			u, ok := s.upstreams[r.Upstream]
			if !ok {
				slog.Warn("unknown upstream, skipping",
					"upstream", r.Upstream,
					"record", r.ID)
				continue
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
			} else {
				slog.Info("record upserted successfully",
					"upstream", r.Upstream,
					"record", r.ID,
					"name", r.Name)
			}
		}

	case source.EventDelete:
		slog.Debug("processing delete event", "source", ev.SourceID)
		records, err := s.store.List()
		if err != nil {
			slog.Error("registry list failed",
				"err", err)
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
			slog.Error("registry delete failed",
				"sourceID", ev.SourceID,
				"err", err)
		} else {
			slog.Info("source records deleted",
				"source", ev.SourceID,
				"count", deleted)
		}
	}
}

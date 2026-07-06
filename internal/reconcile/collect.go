package reconcile

import (
	"context"
	"log/slog"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/source"
)

// CollectorOptions configures a Collector.
type CollectorOptions struct {
	Sources []source.Snapshotter
	// Logger is optional; nil uses slog.Default.
	Logger *slog.Logger
}

// Collector builds the desired record set for a reconcile pass. It snapshots
// every source and caches the last good result per source, so a failed read
// reuses the last known records instead of yielding an empty set. Failed
// sources are also left out of the snapshotted set, so diff never deletes their
// records. This is the runtime half of "no data means no change."
//
// A Collector is owned by the single reconcile goroutine and is not safe for
// concurrent use.
type Collector struct {
	sources  []source.Snapshotter
	logger   *slog.Logger
	lastGood map[string][]model.Record // source name -> last successful snapshot
}

// NewCollector builds a Collector over opts.Sources.
func NewCollector(opts CollectorOptions) *Collector {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Collector{
		sources:  opts.Sources,
		logger:   logger,
		lastGood: make(map[string][]model.Record, len(opts.Sources)),
	}
}

// Collect snapshots every source and returns the merged desired record set plus
// the names of sources that snapshotted cleanly this pass. On a snapshot error
// the source's last good records are reused and its name is omitted from
// snapshotted, so diff treats those records as unchanged rather than deleted.
func (c *Collector) Collect(ctx context.Context) (desired []model.Record, snapshotted map[string]bool) {
	snapshotted = make(map[string]bool, len(c.sources))
	for _, src := range c.sources {
		name := src.Name()
		recs, err := src.Snapshot(ctx)
		if err != nil {
			c.logger.Warn("reconcile: source snapshot failed, keeping last good state",
				"source", name,
				"err", err)
			desired = append(desired, c.lastGood[name]...)
			continue
		}
		c.lastGood[name] = recs
		snapshotted[name] = true
		desired = append(desired, recs...)
	}
	return desired, snapshotted
}

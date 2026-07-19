package source

import (
	"context"

	"github.com/16bitowl/beacons/internal/model"
)

// Snapshotter is the reconciler-facing source contract: a best-effort provider
// of the records a source currently wants, plus a change signal.
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

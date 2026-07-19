package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/16bitowl/beacons/internal/metrics"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// UpstreamCollectorOptions configures an UpstreamCollector.
type UpstreamCollectorOptions struct {
	Upstreams map[string]upstream.Upstream
	// VerifyInterval is the minimum time between List calls per upstream name.
	// A zero or absent entry disables verification for that upstream (default:
	// off) — opt-in, independent of the local reconcile cadence.
	VerifyInterval map[string]time.Duration
	// Metrics is optional; nil disables metrics recording.
	Metrics *metrics.Metrics
	// Logger is optional; nil uses slog.Default.
	Logger *slog.Logger
}

// UpstreamCollector fetches each Lister-capable upstream's actual record set
// for drift detection, throttled independently per upstream so verification
// cadence never scales with local reconcile frequency — a busy Docker host can
// debounce reconciles every few seconds, but a zone-list call must respect the
// provider's own rate limits regardless.
//
// An UpstreamCollector is owned by the single reconcile goroutine and is not
// safe for concurrent use.
type UpstreamCollector struct {
	upstreams map[string]upstream.Upstream
	interval  map[string]time.Duration
	nextCheck map[string]time.Time
	metrics   *metrics.Metrics
	logger    *slog.Logger
}

// NewUpstreamCollector builds an UpstreamCollector over opts.Upstreams.
func NewUpstreamCollector(opts UpstreamCollectorOptions) *UpstreamCollector {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &UpstreamCollector{
		upstreams: opts.Upstreams,
		interval:  opts.VerifyInterval,
		nextCheck: make(map[string]time.Time, len(opts.Upstreams)),
		metrics:   opts.Metrics,
		logger:    logger,
	}
}

// Collect calls List on every upstream due for a check this pass. actual holds
// the fetched records keyed by upstream name; fetched marks which upstreams
// produced fresh data this pass. An upstream that isn't a Lister, isn't due
// yet, or whose List call failed is simply omitted / left false — diff then
// falls back to trusting the store for it, which is always safe, never
// destructive (no last-good cache needed here, unlike the source Collector).
func (c *UpstreamCollector) Collect(ctx context.Context, now time.Time) (actual map[string][]model.Record, fetched map[string]bool) {
	actual = make(map[string][]model.Record, len(c.upstreams))
	fetched = make(map[string]bool, len(c.upstreams))

	for name, u := range c.upstreams {
		lister, ok := u.(upstream.Lister)
		if !ok {
			continue
		}
		interval := c.interval[name]
		if interval <= 0 {
			continue // verification disabled for this upstream; opt-in required
		}
		if next, seen := c.nextCheck[name]; seen && now.Before(next) {
			continue
		}

		start := time.Now()
		records, err := lister.List(ctx)
		c.observe(name, err, time.Since(start))
		if err != nil {
			c.logger.Warn("reconcile: upstream verification list failed, trusting store this pass",
				"upstream", name,
				"err", err)
			continue
		}

		c.nextCheck[name] = now.Add(interval)
		actual[name] = records
		fetched[name] = true
	}

	return actual, fetched
}

func (c *UpstreamCollector) observe(name string, err error, dur time.Duration) {
	if c.metrics == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "failure"
	}
	c.metrics.RecordSync(name, "list", result, dur)
}

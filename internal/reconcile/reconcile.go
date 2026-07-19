package reconcile

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

// Options configures a Reconciler.
type Options struct {
	Store     registry.Store
	Sources   []source.Snapshotter
	Upstreams map[string]upstream.Upstream
	// Interval triggers a periodic full reconcile for self-healing and drift
	// correction; 0 disables the ticker.
	Interval time.Duration
	// DebounceDelay coalesces source notifications before a pass; 0 reconciles
	// on every notification.
	DebounceDelay time.Duration
	// MaxConcurrency bounds per-upstream executor workers; 0 means 1.
	MaxConcurrency int
	// Metrics is optional; nil disables metrics recording.
	Metrics *metrics.Metrics
	// Logger is optional; nil uses slog.Default.
	Logger *slog.Logger
}

// Reconciler drives the declarative loop: collect desired state from sources,
// diff it against recorded state, and apply the minimal set of upstream changes.
// A single goroutine owns recorded state; all concurrency lives in the executor.
type Reconciler struct {
	store     registry.Store
	sources   []source.Snapshotter
	collector *Collector
	executor  *Executor
	interval  time.Duration
	debounce  time.Duration
	logger    *slog.Logger

	listFailures int // consecutive store.List failures, for read backoff
}

// New builds a Reconciler from opts.
func New(opts Options) *Reconciler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Reconciler{
		store:   opts.Store,
		sources: opts.Sources,
		collector: NewCollector(CollectorOptions{
			Sources: opts.Sources,
			Logger:  logger,
		}),
		executor: NewExecutor(ExecutorOptions{
			Store:          opts.Store,
			Upstreams:      opts.Upstreams,
			MaxConcurrency: opts.MaxConcurrency,
			Metrics:        opts.Metrics,
			Logger:         logger,
		}),
		interval: opts.Interval,
		debounce: opts.DebounceDelay,
		logger:   logger,
	}
}

// Run starts every source's change notifier and reconciles until ctx is
// canceled. It runs one reconcile immediately, then on each debounced
// notification and each interval tick.
func (r *Reconciler) Run(ctx context.Context) error {
	notify := make(chan struct{}, 1)
	for _, src := range r.sources {
		go func(s source.Snapshotter) {
			r.logger.Info("starting source", "source", s.Name())
			s.Notify(ctx, notify)
			r.logger.Info("source stopped", "source", s.Name())
		}(src)
	}

	var tickC <-chan time.Time
	if r.interval > 0 {
		t := time.NewTicker(r.interval)
		defer t.Stop()
		tickC = t.C
		r.logger.Info("periodic reconcile enabled", "interval", r.interval)
	}

	r.reconcile(ctx) // initial full reconcile

	// One reusable debounce timer coalesces notification bursts; latest wins.
	var debounce *time.Timer
	var debounceC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler shutting down")
			if debounce != nil {
				debounce.Stop()
			}
			return nil
		case <-notify:
			if r.debounce > 0 {
				if debounce == nil {
					debounce = time.NewTimer(r.debounce)
					debounceC = debounce.C
				} else {
					// Drain a possibly-already-fired timer before Reset so a
					// stale tick can't trigger an extra pass.
					if !debounce.Stop() {
						select {
						case <-debounce.C:
						default:
						}
					}
					debounce.Reset(r.debounce)
				}
				continue
			}
			r.reconcile(ctx)
		case <-debounceC:
			r.reconcile(ctx)
		case <-tickC:
			r.reconcile(ctx)
		}
	}
}

// reconcile runs one pass: collect desired state, diff against the store, apply.
func (r *Reconciler) reconcile(ctx context.Context) {
	// A panic in collect/diff/persist runs on this (main) goroutine; recover so
	// one bad pass is abandoned and logged rather than crashing the process. The
	// executor recovers its own per-op worker panics separately.
	defer func() {
		if p := recover(); p != nil {
			r.logger.Error("reconcile: recovered panic in reconcile pass",
				"panic", p)
		}
	}()
	if ctx.Err() != nil {
		return
	}
	desired, snapshotted := r.collector.Collect(ctx)
	recorded, err := r.store.List()
	if err != nil {
		// A broken store paired with bursty notifications would otherwise hammer
		// the backend on every signal. Back off before the next pass runs.
		r.listFailures++
		d := storeReadBackoff(r.listFailures)
		r.logger.Error("reconcile: failed to read store, backing off",
			"failures", r.listFailures,
			"backoff", d,
			"err", err)
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
		return
	}
	r.listFailures = 0

	// Orphan cleanup: records whose owning source is no longer configured have
	// no live snapshotter to reproduce them, so they never appear in desired.
	// Mark those source names as snapshotted so diff emits deletes for them,
	// clearing both the store and the upstream. Deletion-scoping still protects
	// records of configured sources that merely failed to snapshot this pass.
	markOrphanSources(r.sources, recorded, snapshotted)

	plan := diff(desired, recorded, snapshotted)
	s := plan.Summary()
	r.logger.Info("reconcile pass computed",
		"create", s[OpCreate],
		"update", s[OpUpdate],
		"delete", s[OpDelete],
		"noop", s[OpNoop])

	r.executor.Apply(ctx, plan, recorded)
}

// markOrphanSources flags source names that appear in recorded state but are no
// longer configured, so diff treats their records as deletable.
func markOrphanSources(sources []source.Snapshotter, recorded []model.Record, snapshotted map[string]bool) {
	configured := make(map[string]bool, len(sources))
	for _, s := range sources {
		configured[s.Name()] = true
	}
	for _, rec := range recorded {
		if !configured[rec.SourceName] {
			snapshotted[rec.SourceName] = true
		}
	}
}

// storeReadBackoff maps consecutive store-read failures to a capped delay before
// the next reconcile pass, so a broken store isn't hammered by every notify.
func storeReadBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	const base = time.Second
	const maxDelay = 30 * time.Second
	d := base << uint(min(failures-1, 20))
	if d <= 0 || d > maxDelay {
		return maxDelay
	}
	return d
}

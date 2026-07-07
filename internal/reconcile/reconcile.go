package reconcile

import (
	"context"
	"log/slog"
	"time"

	"github.com/16bitowl/beacons/internal/metrics"
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

	var debounceC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler shutting down")
			return nil
		case <-notify:
			if r.debounce > 0 {
				debounceC = time.After(r.debounce) // coalesce bursts; latest wins
				continue
			}
			r.reconcile(ctx)
		case <-debounceC:
			debounceC = nil
			r.reconcile(ctx)
		case <-tickC:
			r.reconcile(ctx)
		}
	}
}

// reconcile runs one pass: collect desired state, diff against the store, apply.
func (r *Reconciler) reconcile(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	desired, snapshotted := r.collector.Collect(ctx)
	recorded, err := r.store.List()
	if err != nil {
		r.logger.Error("reconcile: failed to read store",
			"err", err)
		return
	}

	plan := diff(desired, recorded, snapshotted)
	s := plan.Summary()
	r.logger.Info("reconcile pass computed",
		"create", s[OpCreate],
		"update", s[OpUpdate],
		"delete", s[OpDelete],
		"noop", s[OpNoop])

	r.executor.Apply(ctx, plan, recorded)
}

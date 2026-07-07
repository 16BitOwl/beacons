package reconcile

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/16bitowl/beacons/internal/metrics"
	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/internal/registry"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// ExecutorOptions configures an Executor.
type ExecutorOptions struct {
	Store     registry.Store
	Upstreams map[string]upstream.Upstream
	// MaxConcurrency bounds per-upstream workers; 0 means 1 (serial).
	// Keep this at 1 until the SessionAuth herd and GetBody mutation are fixed.
	MaxConcurrency int
	// Metrics is optional; nil disables metrics recording.
	Metrics *metrics.Metrics
	// Backoff maps a consecutive-failure count to the minimum delay before the
	// next attempt. nil uses defaultBackoff.
	Backoff func(failures int) time.Duration
	// Now is the clock; nil uses time.Now. Injected for tests.
	Now func() time.Time
	// Logger is optional; nil uses slog.Default.
	Logger *slog.Logger
}

// Executor applies a Plan's ops to upstreams and writes the results back to the
// store. It gates persistently-failing records with an in-memory backoff so a
// broken record does not hammer the upstream every pass; the gate is rebuilt on
// restart, which retries immediately.
//
// An Executor is owned by the single reconcile goroutine. Ops fan out per
// upstream up to MaxConcurrency, but all store writes happen serially after each
// upstream's ops complete, so the store keeps a single writer.
type Executor struct {
	store     registry.Store
	upstreams map[string]upstream.Upstream
	maxConc   int
	metrics   *metrics.Metrics
	backoff   func(failures int) time.Duration
	now       func() time.Time
	logger    *slog.Logger

	nextRetry map[string]time.Time // record key -> earliest next attempt
}

// NewExecutor builds an Executor from opts.
func NewExecutor(opts ExecutorOptions) *Executor {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	backoff := opts.Backoff
	if backoff == nil {
		backoff = defaultBackoff
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	maxConc := opts.MaxConcurrency
	if maxConc < 1 {
		maxConc = 1
	}
	return &Executor{
		store:     opts.Store,
		upstreams: opts.Upstreams,
		maxConc:   maxConc,
		metrics:   opts.Metrics,
		backoff:   backoff,
		now:       now,
		logger:    logger,
		nextRetry: make(map[string]time.Time),
	}
}

// applyResult is the outcome of one op, applied to the store serially after the
// per-upstream fan-out completes.
type applyResult struct {
	record  model.Record
	deleted bool // delete succeeded -> remove from store
	err     error
	skip    bool // noop or gated -> no store write
}

// Apply executes plan against the upstreams and persists results. recorded is
// the store state from this pass, used to carry forward per-record failure
// counts onto desired records (which lack sync-status fields).
func (e *Executor) Apply(ctx context.Context, plan Plan, recorded []model.Record) {
	recordedByKey := make(map[string]model.Record, len(recorded))
	for _, r := range recorded {
		recordedByKey[model.RecordKey(r)] = r
	}
	now := e.now()

	for name, ops := range plan.Ops {
		u, ok := e.upstreams[name]
		if !ok {
			e.applyUnknownUpstream(name, ops)
			continue
		}
		results := e.applyUpstream(ctx, u, ops, recordedByKey, now)
		e.persist(results, now)
	}
}

// applyUpstream runs an upstream's ops with up to maxConc workers and returns
// the results; it performs no store writes.
func (e *Executor) applyUpstream(ctx context.Context, u upstream.Upstream, ops []Op, recordedByKey map[string]model.Record, now time.Time) []applyResult {
	results := make([]applyResult, len(ops))
	sem := make(chan struct{}, e.maxConc)
	var wg sync.WaitGroup

	for i, op := range ops {
		if op.Kind == OpNoop {
			results[i] = applyResult{skip: true}
			continue
		}
		if t, gated := e.nextRetry[model.RecordKey(op.Record)]; gated && now.Before(t) {
			results[i] = applyResult{skip: true}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, op Op) {
			defer wg.Done()
			defer func() { <-sem }()
			prior := recordedByKey[model.RecordKey(op.Record)]
			results[i] = e.do(ctx, u, op, prior)
		}(i, op)
	}
	wg.Wait()
	return results
}

// do applies a single op to the upstream and returns the record with updated
// sync status. It does not touch the store or the backoff map.
func (e *Executor) do(ctx context.Context, u upstream.Upstream, op Op, prior model.Record) applyResult {
	switch op.Kind {
	case OpCreate, OpUpdate:
		r := op.Record
		r.Failures = prior.Failures // carry forward across attempts
		e.logger.Info("reconcile: upserting record",
			"upstream", r.Upstream,
			"record", r.ID,
			"type", r.Type,
			"name", r.Name,
			"value", r.Value)
		err := e.upsert(ctx, u, r)
		if err != nil {
			r.Failures++
			r.Status = model.RecordStatusFailed
			r.SyncError = err.Error()
			e.logger.Error("reconcile: upstream upsert failed",
				"upstream", r.Upstream,
				"record", r.ID,
				"failures", r.Failures,
				"err", err)
			return applyResult{record: r, err: err}
		}
		r.Failures = 0
		r.Status = model.RecordStatusSynced
		r.SyncedAt = e.now()
		r.SyncError = ""
		return applyResult{record: r}

	case OpDelete:
		r := op.Record // recorded record, already carries Failures
		e.logger.Info("reconcile: deleting record",
			"upstream", r.Upstream,
			"record", r.ID,
			"type", r.Type,
			"name", r.Name)
		err := e.delete(ctx, u, r)
		if err != nil {
			r.Failures++
			r.Status = model.RecordStatusPendingDelete
			r.SyncError = err.Error()
			e.logger.Error("reconcile: upstream delete failed",
				"upstream", r.Upstream,
				"record", r.ID,
				"failures", r.Failures,
				"err", err)
			return applyResult{record: r, err: err}
		}
		return applyResult{record: r, deleted: true}

	default:
		return applyResult{skip: true}
	}
}

// persist writes op results back to the store serially and updates the backoff
// map. A failed op sets a retry gate; a success clears it.
func (e *Executor) persist(results []applyResult, now time.Time) {
	for _, res := range results {
		if res.skip {
			continue
		}
		key := model.RecordKey(res.record)
		if res.err != nil {
			e.nextRetry[key] = now.Add(e.backoff(res.record.Failures))
			if err := e.store.Upsert(res.record); err != nil {
				e.logger.Error("reconcile: store upsert failed after apply error",
					"record", res.record.ID,
					"err", err)
			}
			continue
		}
		delete(e.nextRetry, key)
		if res.deleted {
			if err := e.store.DeleteRecord(res.record); err != nil {
				e.logger.Error("reconcile: store delete failed",
					"record", res.record.ID,
					"err", err)
			}
			continue
		}
		if err := e.store.Upsert(res.record); err != nil {
			e.logger.Error("reconcile: store upsert failed",
				"record", res.record.ID,
				"err", err)
		}
	}
}

// applyUnknownUpstream handles ops whose upstream is not configured: deletes
// drop the dangling store entry; creates/updates are skipped with a warning.
func (e *Executor) applyUnknownUpstream(name string, ops []Op) {
	for _, op := range ops {
		switch op.Kind {
		case OpDelete:
			if err := e.store.DeleteRecord(op.Record); err != nil {
				e.logger.Error("reconcile: store delete failed for unknown-upstream record",
					"upstream", name,
					"record", op.Record.ID,
					"err", err)
			}
		case OpCreate, OpUpdate:
			e.logger.Warn("reconcile: unknown upstream, skipping",
				"upstream", name,
				"record", op.Record.ID)
		}
	}
}

func (e *Executor) upsert(ctx context.Context, u upstream.Upstream, r model.Record) error {
	start := e.now()
	err := u.Upsert(ctx, r)
	e.observe(r.Upstream, "upsert", err, start)
	return err
}

func (e *Executor) delete(ctx context.Context, u upstream.Upstream, r model.Record) error {
	start := e.now()
	err := u.Delete(ctx, r)
	e.observe(r.Upstream, "delete", err, start)
	return err
}

func (e *Executor) observe(upstreamName, op string, err error, start time.Time) {
	if e.metrics == nil {
		return
	}
	result := "success"
	if err != nil {
		result = "failure"
	}
	e.metrics.RecordSync(upstreamName, op, result, e.now().Sub(start))
}

// defaultBackoff doubles a 30s base per failure, capped at one hour.
func defaultBackoff(failures int) time.Duration {
	if failures <= 0 {
		return 0
	}
	const base = 30 * time.Second
	const maxDelay = time.Hour
	d := base << uint(min(failures-1, 20))
	if d <= 0 || d > maxDelay {
		return maxDelay
	}
	return d
}

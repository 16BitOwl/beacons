// Package reconcile implements the declarative DNS reconciler: it computes
// desired state from sources, diffs it against recorded state, and applies the
// minimal set of upstream changes. This file defines the plan types; the diff
// that produces them lives in diff.go.
package reconcile

import "github.com/16bitowl/beacons/internal/model"

// OpKind is the action the executor must take for a record.
type OpKind int

const (
	OpNoop   OpKind = iota // recorded matches desired; nothing to do
	OpCreate               // desired only; create on upstream
	OpUpdate               // present in both, but applied fields differ
	OpDelete               // recorded only, and the owning source snapshotted cleanly
)

func (k OpKind) String() string {
	switch k {
	case OpNoop:
		return "noop"
	case OpCreate:
		return "create"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// Op is a single planned change for one record on one upstream. For create,
// update, and noop the record is the desired record; for delete it is the
// recorded record being removed.
type Op struct {
	Kind   OpKind
	Record model.Record
}

// Plan is the set of ops for one reconcile pass, grouped by upstream name so the
// executor can fan out per upstream. OpNoop entries are included for
// observability; the executor skips them.
type Plan struct {
	Ops map[string][]Op
}

// Summary counts ops by kind across all upstreams.
func (p Plan) Summary() map[OpKind]int {
	out := make(map[OpKind]int, 4)
	for _, ops := range p.Ops {
		for _, op := range ops {
			out[op.Kind]++
		}
	}
	return out
}

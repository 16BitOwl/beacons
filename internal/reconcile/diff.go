package reconcile

import "github.com/16bitowl/beacons/internal/model"

// diff computes the plan to move recorded state toward desired state. It is a
// pure function: no I/O and no mutation of its inputs.
//
// snapshotted is the set of source names (model.Record.SourceName) that produced
// a clean snapshot this pass. Deletes are emitted only for records whose owning
// source is in it; records from a source that failed to snapshot are left
// untouched. This is the structural form of "no data means no change" — a source
// read failure can never delete live records.
func diff(desired, recorded []model.Record, snapshotted map[string]bool) Plan {
	desiredByKey := make(map[string]model.Record, len(desired))
	for _, r := range desired {
		desiredByKey[model.RecordKey(r)] = r
	}
	recordedByKey := make(map[string]model.Record, len(recorded))
	for _, r := range recorded {
		recordedByKey[model.RecordKey(r)] = r
	}

	plan := Plan{Ops: make(map[string][]Op)}
	add := func(op Op) {
		plan.Ops[op.Record.Upstream] = append(plan.Ops[op.Record.Upstream], op)
	}

	// Creates, updates, and noops are driven by desired state.
	for key, want := range desiredByKey {
		have, ok := recordedByKey[key]
		switch {
		case !ok:
			add(Op{Kind: OpCreate, Record: want})
		case have.Status == model.RecordStatusFailed:
			add(Op{Kind: OpUpdate, Record: want})
		case appliedEqual(want, have):
			add(Op{Kind: OpNoop, Record: want})
		default:
			add(Op{Kind: OpUpdate, Record: want})
		}
	}

	// Deletes are driven by recorded records absent from desired, scoped to
	// sources that snapshotted cleanly this pass.
	for key, have := range recordedByKey {
		if _, ok := desiredByKey[key]; ok {
			continue
		}
		if !snapshotted[have.SourceName] {
			continue // owning source did not snapshot cleanly; leave untouched
		}
		add(Op{Kind: OpDelete, Record: have})
	}

	return plan
}

// appliedEqual reports whether the applied-relevant fields of two records match.
// SyncedAt, SyncError, and Failures are ignored; Status is checked separately by
// the caller, since a failed record must retry even when these fields match.
// Name is compared because it is a mutable applied field: a changed record name
// must propagate to the upstream.
func appliedEqual(a, b model.Record) bool {
	return a.Type == b.Type &&
		a.Name == b.Name &&
		a.Value == b.Value &&
		a.TTL == b.TTL &&
		a.Priority == b.Priority &&
		a.Comment == b.Comment
}

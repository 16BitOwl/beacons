package reconcile

import (
	"fmt"
	"strings"

	"github.com/16bitowl/beacons/internal/model"
)

// upstreamVerification carries one pass's upstream-verification results into diff.
type upstreamVerification struct {
	// Actual: records each upstream's List returned this pass, keyed by name.
	Actual map[string][]model.Record
	// Fetched: upstreams with fresh data this pass; absent ones are not drift-checked.
	Fetched map[string]bool
	// Compare: per-upstream drift-equality fn; nil falls back to appliedEqual.
	Compare map[string]func(want, got model.Record) bool
}

// diff computes the plan to move recorded state toward desired state. It is a
// pure function: no I/O and no mutation of its inputs.
//
// snapshotted is the set of source names (model.Record.SourceName) that produced
// a clean snapshot this pass. Deletes are emitted only for records whose owning
// source is in it; records from a source that failed to snapshot are left
// untouched. This is the structural form of "no data means no change" — a source
// read failure can never delete live records.
func diff(
	desired, recorded []model.Record,
	snapshotted map[string]bool,
	upstreams upstreamVerification,
) Plan {
	desiredByKey := make(map[string]model.Record, len(desired))
	for _, r := range desired {
		desiredByKey[model.RecordKey(r)] = r
	}
	recordedByKey := make(map[string]model.Record, len(recorded))
	for _, r := range recorded {
		recordedByKey[model.RecordKey(r)] = r
	}
	idx := buildActualIndex(upstreams.Actual)

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
			if op, drifted := driftCorrection(idx, upstreams, want); drifted {
				add(op)
				break
			}
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

// naturalKey identifies a DNS record by its actual content, for matching
// desired state against upstream-fetched state that carries no
// beacons-internal IDs (model.RecordKey is meaningless there).
func naturalKey(upstream string, t model.RecordType, name, value string) string {
	return upstream + "/" + string(t) + "/" + name + "/" + value
}

// nameTypeKey identifies a DNS record by upstream/type/name only, ignoring
// value — used to find whatever an upstream currently holds for a name+type
// pair, to distinguish "nothing there" drift from "wrong value" drift.
func nameTypeKey(upstream string, t model.RecordType, name string) string {
	return upstream + "/" + string(t) + "/" + name
}

// actualIndex indexes one pass's upstream-fetched records for drift matching.
type actualIndex struct {
	exact      map[string]model.Record // naturalKey -> first record with this exact content
	byNameType map[string]model.Record // nameTypeKey -> first record seen for it
}

// buildActualIndex indexes actual (upstream name -> its fetched records) for
// use by driftCorrection. Multiple records can share a name+type (round-robin
// A, multiple MX); both maps keep the first, matching the "update only the
// first" behavior the adapters themselves use for such collisions.
func buildActualIndex(actual map[string][]model.Record) actualIndex {
	idx := actualIndex{
		exact:      make(map[string]model.Record),
		byNameType: make(map[string]model.Record),
	}
	for upstreamName, records := range actual {
		for _, r := range records {
			ek := naturalKey(upstreamName, r.Type, r.Name, r.Value)
			if _, ok := idx.exact[ek]; !ok {
				idx.exact[ek] = r
			}
			ntk := nameTypeKey(upstreamName, r.Type, r.Name)
			if _, ok := idx.byNameType[ntk]; !ok {
				idx.byNameType[ntk] = r
			}
		}
	}
	return idx
}

// driftDetail describes which literal applied fields differ between want and
// the matched upstream record (if any), for debug logging when a drift
// correction fires. hasGot is false for the "missing" case, where there is no
// upstream record to diff against at all. This is diagnostic only — it may
// list a field an upstream's own DriftComparer doesn't actually weigh (e.g. a
// literal comment difference on an upstream that ignores comments), which is
// still useful context for a human reading the log.
func driftDetail(want, got model.Record, hasGot bool) string {
	if !hasGot {
		return fmt.Sprintf("no upstream record for type=%s name=%q value=%q", want.Type, want.Name, want.Value)
	}
	var diffs []string
	if want.Value != got.Value {
		diffs = append(diffs, fmt.Sprintf("value: upstream=%q desired=%q", got.Value, want.Value))
	}
	if want.TTL != got.TTL {
		diffs = append(diffs, fmt.Sprintf("ttl: upstream=%d desired=%d", got.TTL, want.TTL))
	}
	if want.Priority != got.Priority {
		diffs = append(diffs, fmt.Sprintf("priority: upstream=%d desired=%d", got.Priority, want.Priority))
	}
	if want.Comment != got.Comment {
		diffs = append(diffs, fmt.Sprintf("comment: upstream=%q desired=%q", got.Comment, want.Comment))
	}
	if len(diffs) == 0 {
		return "no literal field difference (drift decided by an upstream-specific comparison rule)"
	}
	return strings.Join(diffs, "; ")
}

// driftCorrection reports whether upstream-fetched state disagrees with the
// store's belief that want is synced, and if so the self-healing op. Only acts
// when upstreams.Fetched[want.Upstream] is true; otherwise the store is trusted.
// An exact-content (naturalKey) hit is still re-checked with cmp, since fields
// like TTL/Priority can drift and round-robin records must match their own entry.
func driftCorrection(idx actualIndex, upstreams upstreamVerification, want model.Record) (Op, bool) {
	if !upstreams.Fetched[want.Upstream] {
		return Op{}, false
	}
	cmp := upstreams.Compare[want.Upstream]
	if cmp == nil {
		cmp = appliedEqual // upstream doesn't customize: same field set as the two-way diff
	}

	if got, ok := idx.exact[naturalKey(want.Upstream, want.Type, want.Name, want.Value)]; ok {
		if cmp(want, got) {
			return Op{}, false
		}
		return Op{Kind: OpUpdate, Record: want, DriftReason: DriftChanged, DriftDetail: driftDetail(want, got, true)}, true
	}
	// A name+type match here always differs in Value (an exact match would
	// have hit the branch above), so it's unconditionally a value change.
	if got, ok := idx.byNameType[nameTypeKey(want.Upstream, want.Type, want.Name)]; ok {
		return Op{Kind: OpUpdate, Record: want, DriftReason: DriftChanged, DriftDetail: driftDetail(want, got, true)}, true
	}
	return Op{Kind: OpCreate, Record: want, DriftReason: DriftMissing, DriftDetail: driftDetail(want, model.Record{}, false)}, true
}

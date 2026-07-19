package reconcile

import (
	"strings"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
)

// rec builds a record with the common identity fields set. Optional mutators
// tweak applied or status fields for a specific case.
func rec(source, id, upstream string, muts ...func(*model.Record)) model.Record {
	r := model.Record{
		ID:         id,
		SourceID:   source + ":" + id,
		SourceName: source,
		Upstream:   upstream,
		Type:       model.RecordTypeA,
		Name:       id + ".example.com",
		Value:      "10.0.0.1",
		BaseRecord: model.BaseRecord{TTL: 300},
	}
	for _, m := range muts {
		m(&r)
	}
	return r
}

// kindByKey flattens a plan into RecordKey -> OpKind for order-independent
// assertions. It fails if the same key appears twice.
func kindByKey(t *testing.T, p Plan) map[string]OpKind {
	t.Helper()
	out := make(map[string]OpKind)
	for _, ops := range p.Ops {
		for _, op := range ops {
			k := model.RecordKey(op.Record)
			if _, dup := out[k]; dup {
				t.Fatalf("duplicate op for key %q", k)
			}
			out[k] = op.Kind
		}
	}
	return out
}

func TestDiff_CreateUpdateNoopDelete(t *testing.T) {
	snapshotted := map[string]bool{"docker": true}

	same := rec("docker", "web", "cloudflare")
	changed := rec("docker", "api", "cloudflare", func(r *model.Record) { r.Value = "10.0.0.9" })
	recordedChanged := rec("docker", "api", "cloudflare") // old value 10.0.0.1
	newRec := rec("docker", "new", "cloudflare")
	orphan := rec("docker", "gone", "cloudflare")

	desired := []model.Record{same, changed, newRec}
	recorded := []model.Record{same, recordedChanged, orphan}

	got := kindByKey(t, diff(desired, recorded, snapshotted, upstreamVerification{}))

	assertKind := func(r model.Record, want OpKind) {
		if k := got[model.RecordKey(r)]; k != want {
			t.Errorf("%s: got %v, want %v", model.RecordKey(r), k, want)
		}
	}
	assertKind(same, OpNoop)
	assertKind(changed, OpUpdate)
	assertKind(newRec, OpCreate)
	assertKind(orphan, OpDelete)
	if len(got) != 4 {
		t.Errorf("op count: got %d, want 4 (%v)", len(got), got)
	}
}

func TestDiff_DeleteSuppressedWhenSourceNotSnapshotted(t *testing.T) {
	// The transient-failure guarantee: a source absent from snapshotted must
	// never have its records deleted, even when they are missing from desired.
	orphan := rec("yaml", "gone", "pihole")
	recorded := []model.Record{orphan}

	// yaml failed to snapshot this pass -> not in the set.
	got := diff(nil, recorded, map[string]bool{"docker": true}, upstreamVerification{})

	if n := len(got.Summary()); n != 0 {
		t.Fatalf("expected no ops when owning source did not snapshot, got %v", got.Ops)
	}
}

func TestDiff_DeleteEmittedOnlyForCleanSource(t *testing.T) {
	// Two orphans from different sources; only the snapshotted source's orphan
	// is deleted.
	orphanClean := rec("docker", "a", "cloudflare")
	orphanDirty := rec("yaml", "b", "cloudflare")
	recorded := []model.Record{orphanClean, orphanDirty}

	got := kindByKey(t, diff(nil, recorded, map[string]bool{"docker": true}, upstreamVerification{}))

	if got[model.RecordKey(orphanClean)] != OpDelete {
		t.Errorf("clean source orphan: got %v, want delete", got[model.RecordKey(orphanClean)])
	}
	if _, ok := got[model.RecordKey(orphanDirty)]; ok {
		t.Errorf("dirty source orphan should be left untouched, got %v", got[model.RecordKey(orphanDirty)])
	}
}

func TestDiff_FieldSensitivity(t *testing.T) {
	base := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	cases := []struct {
		name string
		mut  func(*model.Record)
		want OpKind
	}{
		{"type", func(r *model.Record) { r.Type = model.RecordTypeAAAA }, OpUpdate},
		{"name", func(r *model.Record) { r.Name = "other.example.com" }, OpUpdate},
		{"value", func(r *model.Record) { r.Value = "10.0.0.2" }, OpUpdate},
		{"ttl", func(r *model.Record) { r.TTL = 60 }, OpUpdate},
		{"priority", func(r *model.Record) { r.Priority = 10 }, OpUpdate},
		{"comment", func(r *model.Record) { r.Comment = "changed" }, OpUpdate},
		// Sync-status fields must be ignored -> noop.
		{"status", func(r *model.Record) { r.Status = model.RecordStatusFailed }, OpNoop},
		{"failures", func(r *model.Record) { r.Failures = 3 }, OpNoop},
		{"sync_error", func(r *model.Record) { r.SyncError = "boom" }, OpNoop},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desiredRec := rec("docker", "web", "cloudflare", tc.mut)
			got := kindByKey(t, diff([]model.Record{desiredRec}, []model.Record{base}, snapshotted, upstreamVerification{}))
			if got[model.RecordKey(base)] != tc.want {
				t.Errorf("field %q: got %v, want %v", tc.name, got[model.RecordKey(base)], tc.want)
			}
		})
	}
}

func TestDiff_GroupsByUpstream(t *testing.T) {
	cf := rec("docker", "web", "cloudflare")
	ph := rec("docker", "web", "pihole")

	plan := diff([]model.Record{cf, ph}, nil, map[string]bool{"docker": true}, upstreamVerification{})

	if len(plan.Ops["cloudflare"]) != 1 || plan.Ops["cloudflare"][0].Kind != OpCreate {
		t.Errorf("cloudflare group: %+v", plan.Ops["cloudflare"])
	}
	if len(plan.Ops["pihole"]) != 1 || plan.Ops["pihole"][0].Kind != OpCreate {
		t.Errorf("pihole group: %+v", plan.Ops["pihole"])
	}
}

func TestDiff_EmptyInputs(t *testing.T) {
	plan := diff(nil, nil, nil, upstreamVerification{})
	if len(plan.Ops) != 0 {
		t.Errorf("expected empty plan, got %v", plan.Ops)
	}
}

func TestDiff_FailedRecordRetriesEvenWhenFieldsMatch(t *testing.T) {
	// A create/update that never reached the upstream still gets an optimistic
	// store write (Status=failed) with applied fields equal to desired. That
	// must not settle into noop, or the record is never retried again.
	want := rec("docker", "web", "cloudflare")
	have := rec("docker", "web", "cloudflare", func(r *model.Record) {
		r.Status = model.RecordStatusFailed
	})

	got := kindByKey(t, diff([]model.Record{want}, []model.Record{have}, map[string]bool{"docker": true}, upstreamVerification{}))
	if got[model.RecordKey(want)] != OpUpdate {
		t.Errorf("failed recorded status: got %v, want update", got[model.RecordKey(want)])
	}
}

// ---------------------------------------------------------------------------
// Three-way diff: upstream verification / drift correction
// ---------------------------------------------------------------------------

func TestDiff_DriftMissing_CorrectsAsCreate(t *testing.T) {
	// Store believes web is synced, but the upstream's fetched state holds no
	// record for it at all (deleted by hand) — self-heal via create.
	same := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpCreate {
		t.Fatalf("kind: got %v, want create", got[model.RecordKey(same)])
	}
	op := onlyOp(t, plan, "cloudflare")
	if op.DriftReason != DriftMissing {
		t.Errorf("DriftReason: got %q, want %q", op.DriftReason, DriftMissing)
	}
}

func TestDiff_DriftChanged_CorrectsAsUpdate(t *testing.T) {
	// Store believes web is synced, but the upstream holds a different value
	// for the same name+type (hand-edited) — self-heal via update.
	same := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	actualRec := same
	actualRec.Value = "10.0.0.99"
	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {actualRec}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpUpdate {
		t.Fatalf("kind: got %v, want update", got[model.RecordKey(same)])
	}
	op := onlyOp(t, plan, "cloudflare")
	if op.DriftReason != DriftChanged {
		t.Errorf("DriftReason: got %q, want %q", op.DriftReason, DriftChanged)
	}
}

func TestDiff_DriftSuppressed_WhenNotFetched(t *testing.T) {
	// Upstream disagrees, but this pass didn't fetch it (not due, not a
	// Lister, or List failed) — must fall back to trusting the store: noop.
	same := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual: map[string][]model.Record{"cloudflare": {}},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpNoop {
		t.Errorf("kind: got %v, want noop (verification not fetched this pass)", got[model.RecordKey(same)])
	}
}

func TestDiff_NoDriftWhenUpstreamMatches(t *testing.T) {
	same := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {same}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpNoop {
		t.Errorf("kind: got %v, want noop", got[model.RecordKey(same)])
	}
}

func TestDiff_DriftTTLChanged_CorrectsAsUpdate(t *testing.T) {
	// Same value, but the upstream now holds a different TTL — must be caught
	// same as the two-way diff would catch a TTL difference. cloudflare has no
	// DriftComparer override, so this exercises the appliedEqual fallback.
	same := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	actualRec := same
	actualRec.TTL = 60
	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {actualRec}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpUpdate {
		t.Fatalf("kind: got %v, want update (TTL drift)", got[model.RecordKey(same)])
	}
	if op := onlyOp(t, plan, "cloudflare"); op.DriftReason != DriftChanged {
		t.Errorf("DriftReason: got %q, want %q", op.DriftReason, DriftChanged)
	}
}

func TestDiff_DriftPriorityChanged_CorrectsAsUpdate(t *testing.T) {
	same := rec("docker", "mx", "cloudflare", func(r *model.Record) {
		r.Type = model.RecordTypeMX
		r.Priority = 10
	})
	snapshotted := map[string]bool{"docker": true}

	actualRec := same
	actualRec.Priority = 20
	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {actualRec}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpUpdate {
		t.Fatalf("kind: got %v, want update (priority drift)", got[model.RecordKey(same)])
	}
}

func TestDiff_DriftCommentChanged_CorrectsAsUpdate(t *testing.T) {
	// An upstream that does round-trip comments (Cloudflare) must still catch
	// a real cleared/changed comment.
	same := rec("docker", "web", "cloudflare", func(r *model.Record) { r.Comment = "managed by beacons" })
	snapshotted := map[string]bool{"docker": true}

	actualRec := same
	actualRec.Comment = "hand-edited"
	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {actualRec}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpUpdate {
		t.Fatalf("kind: got %v, want update (comment drift)", got[model.RecordKey(same)])
	}
}

func TestDiff_UsesUpstreamComparerOverride(t *testing.T) {
	// diff must not hardcode any per-field carve-out itself — an upstream's own
	// DriftComparer decides which applied fields are meaningful for it. Here a
	// fake comparer ignores TTL entirely; a TTL-only difference must be a noop.
	same := rec("docker", "web", "pihole")
	snapshotted := map[string]bool{"docker": true}

	actualRec := same
	actualRec.TTL = 0 // e.g. an upstream that can't represent TTL for this record
	ignoreTTL := func(want, got model.Record) bool {
		return want.Type == got.Type && want.Name == got.Name && want.Value == got.Value
	}

	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"pihole": {actualRec}},
		Fetched: map[string]bool{"pihole": true},
		Compare: map[string]func(model.Record, model.Record) bool{"pihole": ignoreTTL},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpNoop {
		t.Errorf("kind: got %v, want noop (comparer override ignores TTL)", got[model.RecordKey(same)])
	}
}

func TestDiff_FallsBackToAppliedEqual_WhenNoComparerConfigured(t *testing.T) {
	// An upstream absent from Compare (doesn't implement DriftComparer) is
	// compared on every applied field, same as the two-way diff.
	same := rec("docker", "web", "cloudflare")
	snapshotted := map[string]bool{"docker": true}

	actualRec := same
	actualRec.TTL = 60
	plan := diff([]model.Record{same}, []model.Record{same}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {actualRec}},
		Fetched: map[string]bool{"cloudflare": true},
		Compare: map[string]func(model.Record, model.Record) bool{}, // no entry for cloudflare
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(same)] != OpUpdate {
		t.Errorf("kind: got %v, want update (no comparer configured -> appliedEqual fallback catches TTL drift)", got[model.RecordKey(same)])
	}
}

func TestDriftDetail_MissingRecord(t *testing.T) {
	want := rec("docker", "web", "cloudflare")
	got := driftDetail(want, model.Record{}, false)
	if !strings.Contains(got, "no upstream record") {
		t.Errorf("driftDetail = %q, want it to say no upstream record found", got)
	}
}

func TestDriftDetail_ListsDifferingFields(t *testing.T) {
	want := rec("docker", "web", "cloudflare", func(r *model.Record) { r.TTL = 300 })
	got := want
	got.Value = "10.0.0.9"
	got.TTL = 60

	detail := driftDetail(want, got, true)
	if !strings.Contains(detail, "value: upstream=\"10.0.0.9\" desired=\"10.0.0.1\"") {
		t.Errorf("driftDetail = %q, missing expected value diff", detail)
	}
	if !strings.Contains(detail, "ttl: upstream=60 desired=300") {
		t.Errorf("driftDetail = %q, missing expected ttl diff", detail)
	}
}

func TestDiff_DriftIgnoredWhenAlreadyDiffering(t *testing.T) {
	// A record that already needs an ordinary update (fields differ) must not
	// be re-labeled as a drift correction just because verification also ran.
	want := rec("docker", "web", "cloudflare", func(r *model.Record) { r.Value = "10.0.0.2" })
	have := rec("docker", "web", "cloudflare") // old value 10.0.0.1
	snapshotted := map[string]bool{"docker": true}

	plan := diff([]model.Record{want}, []model.Record{have}, snapshotted, upstreamVerification{
		Actual:  map[string][]model.Record{"cloudflare": {}},
		Fetched: map[string]bool{"cloudflare": true},
	})
	got := kindByKey(t, plan)

	if got[model.RecordKey(want)] != OpUpdate {
		t.Fatalf("kind: got %v, want update", got[model.RecordKey(want)])
	}
	if op := onlyOp(t, plan, "cloudflare"); op.DriftReason != "" {
		t.Errorf("DriftReason: got %q, want empty for an ordinary update", op.DriftReason)
	}
}

// onlyOp returns the single op for upstreamName in plan, failing the test if
// there isn't exactly one.
func onlyOp(t *testing.T, plan Plan, upstreamName string) Op {
	t.Helper()
	ops := plan.Ops[upstreamName]
	if len(ops) != 1 {
		t.Fatalf("ops for %q: got %d, want 1 (%+v)", upstreamName, len(ops), ops)
	}
	return ops[0]
}

func TestDiff_CachedLastGoodProducesNoop(t *testing.T) {
	// A source that failed this pass still contributes its last-good snapshot to
	// desired (the reconciler retains it). Because it equals recorded, the pass
	// is a noop and nothing is deleted despite the source being absent from
	// snapshotted.
	r := rec("yaml", "web", "pihole")
	got := kindByKey(t, diff([]model.Record{r}, []model.Record{r}, map[string]bool{"docker": true}, upstreamVerification{}))
	if got[model.RecordKey(r)] != OpNoop {
		t.Errorf("cached last-good: got %v, want noop", got[model.RecordKey(r)])
	}
}

package reconcile

import (
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

	got := kindByKey(t, diff(desired, recorded, snapshotted))

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
	got := diff(nil, recorded, map[string]bool{"docker": true})

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

	got := kindByKey(t, diff(nil, recorded, map[string]bool{"docker": true}))

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
			got := kindByKey(t, diff([]model.Record{desiredRec}, []model.Record{base}, snapshotted))
			if got[model.RecordKey(base)] != tc.want {
				t.Errorf("field %q: got %v, want %v", tc.name, got[model.RecordKey(base)], tc.want)
			}
		})
	}
}

func TestDiff_GroupsByUpstream(t *testing.T) {
	cf := rec("docker", "web", "cloudflare")
	ph := rec("docker", "web", "pihole")

	plan := diff([]model.Record{cf, ph}, nil, map[string]bool{"docker": true})

	if len(plan.Ops["cloudflare"]) != 1 || plan.Ops["cloudflare"][0].Kind != OpCreate {
		t.Errorf("cloudflare group: %+v", plan.Ops["cloudflare"])
	}
	if len(plan.Ops["pihole"]) != 1 || plan.Ops["pihole"][0].Kind != OpCreate {
		t.Errorf("pihole group: %+v", plan.Ops["pihole"])
	}
}

func TestDiff_EmptyInputs(t *testing.T) {
	plan := diff(nil, nil, nil)
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

	got := kindByKey(t, diff([]model.Record{want}, []model.Record{have}, map[string]bool{"docker": true}))
	if got[model.RecordKey(want)] != OpUpdate {
		t.Errorf("failed recorded status: got %v, want update", got[model.RecordKey(want)])
	}
}

func TestDiff_CachedLastGoodProducesNoop(t *testing.T) {
	// A source that failed this pass still contributes its last-good snapshot to
	// desired (the reconciler retains it). Because it equals recorded, the pass
	// is a noop and nothing is deleted despite the source being absent from
	// snapshotted.
	r := rec("yaml", "web", "pihole")
	got := kindByKey(t, diff([]model.Record{r}, []model.Record{r}, map[string]bool{"docker": true}))
	if got[model.RecordKey(r)] != OpNoop {
		t.Errorf("cached last-good: got %v, want noop", got[model.RecordKey(r)])
	}
}

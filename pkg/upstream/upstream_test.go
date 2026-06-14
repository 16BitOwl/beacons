package upstream_test

import (
	"context"
	"testing"

	"github.com/16bitowl/beacons/internal/model"
	"github.com/16bitowl/beacons/pkg/upstream"
)

// stubbedUpstream is a minimal Upstream that records calls.
type stubbedUpstream struct {
	name        string
	upsertCalls []model.Record
	deleteCalls []model.Record
}

func (s *stubbedUpstream) Name() string { return s.name }

func (s *stubbedUpstream) Upsert(_ context.Context, r model.Record) error {
	s.upsertCalls = append(s.upsertCalls, r)
	return nil
}

func (s *stubbedUpstream) Delete(_ context.Context, r model.Record) error {
	s.deleteCalls = append(s.deleteCalls, r)
	return nil
}

func validRecord() model.Record {
	return model.Record{
		ID:       "web",
		Type:     model.RecordTypeA,
		Name:     "svc.example.com",
		Value:    "1.2.3.4",
		Upstream: "cf",
	}
}

// ---------------------------------------------------------------------------
// DryRun
// ---------------------------------------------------------------------------

func TestDryRun_Name_DelegatesWrapped(t *testing.T) {
	inner := &stubbedUpstream{name: "cf-prod"}
	dr := upstream.NewDryRun(inner)
	if dr.Name() != "cf-prod" {
		t.Errorf("Name() = %q, want cf-prod", dr.Name())
	}
}

func TestDryRun_Upsert_DoesNotCallWrapped(t *testing.T) {
	inner := &stubbedUpstream{name: "cf"}
	dr := upstream.NewDryRun(inner)
	if err := dr.Upsert(context.Background(), validRecord()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(inner.upsertCalls) != 0 {
		t.Errorf("inner.Upsert should not be called in dry-run mode, got %d calls", len(inner.upsertCalls))
	}
}

func TestDryRun_Delete_DoesNotCallWrapped(t *testing.T) {
	inner := &stubbedUpstream{name: "cf"}
	dr := upstream.NewDryRun(inner)
	if err := dr.Delete(context.Background(), validRecord()); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(inner.deleteCalls) != 0 {
		t.Errorf("inner.Delete should not be called in dry-run mode, got %d calls", len(inner.deleteCalls))
	}
}

func TestDryRun_Upsert_ReturnsNil(t *testing.T) {
	dr := upstream.NewDryRun(&stubbedUpstream{name: "x"})
	if err := dr.Upsert(context.Background(), validRecord()); err != nil {
		t.Errorf("Upsert should return nil, got %v", err)
	}
}

func TestDryRun_Delete_ReturnsNil(t *testing.T) {
	dr := upstream.NewDryRun(&stubbedUpstream{name: "x"})
	if err := dr.Delete(context.Background(), validRecord()); err != nil {
		t.Errorf("Delete should return nil, got %v", err)
	}
}

func TestDryRun_ImplementsInterface(t *testing.T) {
	// Compile-time check that DryRun implements upstream.Upstream.
	var _ upstream.Upstream = upstream.NewDryRun(&stubbedUpstream{})
}

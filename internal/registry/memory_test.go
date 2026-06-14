package registry_test

import (
	"testing"

	"github.com/16bitowl/beacons/internal/registry"
)

func TestMemoryStore_ListEmpty(t *testing.T) {
	s := registry.NewMemoryStore()
	records, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("List len = %d, want 0", len(records))
	}
}

func TestMemoryStore_UpsertAndList(t *testing.T) {
	s := registry.NewMemoryStore()
	if err := s.Upsert(newRecord("src1", "web", "cf")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	records, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("List len = %d, want 1", len(records))
	}
	if records[0].ID != "web" {
		t.Errorf("ID = %q, want web", records[0].ID)
	}
}

func TestMemoryStore_UpsertOverwrites(t *testing.T) {
	s := registry.NewMemoryStore()
	r1 := newRecord("src1", "web", "cf")
	r1.Value = "1.1.1.1"
	_ = s.Upsert(r1)

	r2 := newRecord("src1", "web", "cf")
	r2.Value = "2.2.2.2"
	_ = s.Upsert(r2)

	records, _ := s.List()
	if len(records) != 1 {
		t.Fatalf("expected 1 record after overwrite, got %d", len(records))
	}
	if records[0].Value != "2.2.2.2" {
		t.Errorf("Value = %q, want 2.2.2.2", records[0].Value)
	}
}

func TestMemoryStore_SameIDDifferentUpstreams_BothStored(t *testing.T) {
	s := registry.NewMemoryStore()
	_ = s.Upsert(newRecord("src1", "web", "cf"))
	_ = s.Upsert(newRecord("src1", "web", "pihole"))

	records, _ := s.List()
	if len(records) != 2 {
		t.Errorf("expected 2 records for same ID different upstreams, got %d", len(records))
	}
}

func TestMemoryStore_MultipleSourcesSameKey_BothStored(t *testing.T) {
	s := registry.NewMemoryStore()
	_ = s.Upsert(newRecord("src1", "web", "cf"))
	_ = s.Upsert(newRecord("src2", "web", "cf"))

	records, _ := s.List()
	if len(records) != 2 {
		t.Errorf("expected 2 records, got %d", len(records))
	}
}

func TestMemoryStore_Delete_RemovesTargetSource(t *testing.T) {
	s := registry.NewMemoryStore()
	_ = s.Upsert(newRecord("src1", "web", "cf"))
	_ = s.Upsert(newRecord("src1", "api", "cf"))
	_ = s.Upsert(newRecord("src2", "db", "cf"))

	if err := s.Delete("src1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	records, _ := s.List()
	if len(records) != 1 {
		t.Fatalf("List len = %d, want 1", len(records))
	}
	if records[0].SourceID != "src2" {
		t.Errorf("remaining record should be from src2, got %q", records[0].SourceID)
	}
}

func TestMemoryStore_Delete_NonExistentIsNoop(t *testing.T) {
	s := registry.NewMemoryStore()
	if err := s.Delete("nonexistent"); err != nil {
		t.Errorf("Delete nonexistent: %v", err)
	}
}

func TestMemoryStore_Delete_LeavesOtherSourcesIntact(t *testing.T) {
	s := registry.NewMemoryStore()
	_ = s.Upsert(newRecord("src1", "web", "cf"))
	_ = s.Upsert(newRecord("src2", "api", "cf"))
	_ = s.Upsert(newRecord("src3", "db", "cf"))
	_ = s.Delete("src2")

	records, _ := s.List()
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	for _, r := range records {
		if r.SourceID == "src2" {
			t.Errorf("src2 should have been deleted")
		}
	}
}

func TestMemoryStore_ListBySourceName_MatchesOnly(t *testing.T) {
	s := registry.NewMemoryStore()
	r1 := newRecord("src1", "web", "cf")
	r1.SourceName = "docker"
	r2 := newRecord("src2", "api", "cf")
	r2.SourceName = "docker"
	r3 := newRecord("src3", "db", "cf")
	r3.SourceName = "yaml"
	_ = s.Upsert(r1)
	_ = s.Upsert(r2)
	_ = s.Upsert(r3)

	got, err := s.ListBySourceName("docker")
	if err != nil {
		t.Fatalf("ListBySourceName: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListBySourceName(docker) = %d, want 2", len(got))
	}
}

func TestMemoryStore_ListBySourceName_Empty(t *testing.T) {
	s := registry.NewMemoryStore()
	got, err := s.ListBySourceName("nobody")
	if err != nil {
		t.Fatalf("ListBySourceName: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 records, got %d", len(got))
	}
}

func TestMemoryStore_ListBySourceName_NotAffectedByDelete(t *testing.T) {
	s := registry.NewMemoryStore()
	r1 := newRecord("src1", "web", "cf")
	r1.SourceName = "docker"
	r2 := newRecord("src2", "api", "cf")
	r2.SourceName = "docker"
	_ = s.Upsert(r1)
	_ = s.Upsert(r2)
	_ = s.Delete("src1")

	got, _ := s.ListBySourceName("docker")
	if len(got) != 1 {
		t.Errorf("expected 1 record after delete, got %d", len(got))
	}
	if got[0].SourceID != "src2" {
		t.Errorf("remaining record SourceID = %q, want src2", got[0].SourceID)
	}
}

package registry_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/16bitowl/beacons/internal/registry"
)

func tempStorePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "store.json")
}

func TestFileStore_MissingFileIsOK(t *testing.T) {
	s, err := registry.NewFileStore(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	records, _ := s.List()
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestFileStore_InvalidJSONReturnsError(t *testing.T) {
	path := tempStorePath(t)
	if err := os.WriteFile(path, []byte("not json {{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := registry.NewFileStore(path)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestFileStore_UpsertAndList(t *testing.T) {
	s, err := registry.NewFileStore(tempStorePath(t))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
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
}

func TestFileStore_UpsertOverwrites(t *testing.T) {
	s, err := registry.NewFileStore(tempStorePath(t))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
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

func TestFileStore_Persistence(t *testing.T) {
	path := tempStorePath(t)
	s1, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_ = s1.Upsert(newRecord("src1", "web", "cf"))
	_ = s1.Upsert(newRecord("src2", "api", "pihole"))

	s2, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore reload: %v", err)
	}
	records, err := s2.List()
	if err != nil {
		t.Fatalf("List after reload: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("expected 2 records after reload, got %d", len(records))
	}
}

func TestFileStore_Delete_RemovesTargetSource(t *testing.T) {
	s, err := registry.NewFileStore(tempStorePath(t))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_ = s.Upsert(newRecord("src1", "web", "cf"))
	_ = s.Upsert(newRecord("src1", "api", "cf"))
	_ = s.Upsert(newRecord("src2", "db", "cf"))

	if err := s.Delete("src1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	records, _ := s.List()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].SourceID != "src2" {
		t.Errorf("remaining SourceID = %q, want src2", records[0].SourceID)
	}
}

func TestFileStore_DeletePersists(t *testing.T) {
	path := tempStorePath(t)
	s, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_ = s.Upsert(newRecord("src1", "web", "cf"))
	_ = s.Upsert(newRecord("src2", "api", "cf"))
	_ = s.Delete("src1")

	s2, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore reload: %v", err)
	}
	records, _ := s2.List()
	if len(records) != 1 {
		t.Errorf("expected 1 record after reload, got %d", len(records))
	}
	if records[0].SourceID != "src2" {
		t.Errorf("remaining SourceID = %q, want src2", records[0].SourceID)
	}
}

func TestFileStore_ListBySourceName(t *testing.T) {
	s, err := registry.NewFileStore(tempStorePath(t))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	r1 := newRecord("src1", "web", "cf")
	r1.SourceName = "docker"
	r2 := newRecord("src2", "api", "cf")
	r2.SourceName = "yaml"
	_ = s.Upsert(r1)
	_ = s.Upsert(r2)

	got, err := s.ListBySourceName("docker")
	if err != nil {
		t.Fatalf("ListBySourceName: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("ListBySourceName(docker) = %d, want 1", len(got))
	}
	if got[0].ID != "web" {
		t.Errorf("ID = %q, want web", got[0].ID)
	}
}

func TestFileStore_Batch_DefersFlushUntilDone(t *testing.T) {
	path := tempStorePath(t)
	s, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	var sawEmptyMidBatch bool
	err = s.Batch(func() error {
		if err := s.Upsert(newRecord("src1", "web", "cf")); err != nil {
			return err
		}
		if err := s.Upsert(newRecord("src2", "api", "cf")); err != nil {
			return err
		}
		data, readErr := os.ReadFile(path)
		sawEmptyMidBatch = os.IsNotExist(readErr) || len(data) == 0
		return nil
	})
	if err != nil {
		t.Fatalf("Batch: %v", err)
	}
	if !sawEmptyMidBatch {
		t.Error("expected no disk flush while Batch is in progress")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile after Batch: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected exactly one flush to disk once Batch completes")
	}

	records, _ := s.List()
	if len(records) != 2 {
		t.Fatalf("expected 2 records after Batch, got %d", len(records))
	}
}

func TestFileStore_Batch_FlushesWhatWasWrittenBeforeFnError(t *testing.T) {
	path := tempStorePath(t)
	s, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	wantErr := errors.New("boom")
	err = s.Batch(func() error {
		_ = s.Upsert(newRecord("src1", "web", "cf"))
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Batch error = %v, want %v", err, wantErr)
	}

	s2, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore reload: %v", err)
	}
	records, _ := s2.List()
	if len(records) != 1 {
		t.Errorf("expected the pre-error write to still be flushed, got %d records", len(records))
	}
}

func TestFileStore_ListBySourceName_SurvivesReload(t *testing.T) {
	path := tempStorePath(t)
	s, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	r := newRecord("src1", "web", "cf")
	r.SourceName = "docker"
	_ = s.Upsert(r)

	s2, err := registry.NewFileStore(path)
	if err != nil {
		t.Fatalf("NewFileStore reload: %v", err)
	}
	got, _ := s2.ListBySourceName("docker")
	if len(got) != 1 {
		t.Errorf("expected 1 record after reload, got %d", len(got))
	}
}

package registry

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"

	"github.com/16bitowl/beacons/internal/model"
)

// Store is the interface for record persistence.
// Implementations can be in-memory, flat-file, SQLite, etc.
type Store interface {
	// Upsert adds or updates a record keyed by (sourceID, recordID, upstream).
	Upsert(r model.Record) error

	// Delete removes all records for a given sourceID.
	Delete(sourceID string) error

	// List returns all currently stored records.
	List() ([]model.Record, error)
}

func recordKey(r model.Record) string {
	return r.SourceID + "/" + r.ID + "/" + r.Upstream
}

// MemoryStore is a simple in-memory Store implementation.
type MemoryStore struct {
	mu      sync.RWMutex
	records map[string]model.Record
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: make(map[string]model.Record)}
}

func (m *MemoryStore) Upsert(r model.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[recordKey(r)] = r
	return nil
}

func (m *MemoryStore) Delete(sourceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.records {
		if r.SourceID == sourceID {
			delete(m.records, k)
		}
	}
	return nil
}

func (m *MemoryStore) List() ([]model.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Record, 0, len(m.records))
	for _, r := range m.records {
		out = append(out, r)
	}
	return out, nil
}

// FileStore is a persistent Store that writes state to a JSON file.
// It keeps an in-memory map as the working set and flushes to disk on every write.
type FileStore struct {
	mu      sync.RWMutex
	path    string
	records map[string]model.Record
}

func NewFileStore(path string) (*FileStore, error) {
	fs := &FileStore{path: path, records: make(map[string]model.Record)}
	if err := fs.load(); err != nil {
		return nil, err
	}
	slog.Info("file store initialised", "path", path, "records", len(fs.records))
	return fs, nil
}

func (f *FileStore) Upsert(r model.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[recordKey(r)] = r
	return f.flush()
}

func (f *FileStore) Delete(sourceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, r := range f.records {
		if r.SourceID == sourceID {
			delete(f.records, k)
		}
	}
	return f.flush()
}

func (f *FileStore) List() ([]model.Record, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]model.Record, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

// load reads existing state from disk, ignoring a missing file.
func (f *FileStore) load() error {
	data, err := os.ReadFile(f.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &f.records)
}

// flush writes the current state to disk atomically via a temp file.
func (f *FileStore) flush() error {
	data, err := json.Marshal(f.records)
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

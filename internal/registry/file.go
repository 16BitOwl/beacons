package registry

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"

	"github.com/16bitowl/beacons/internal/model"
)

// FileStore is a persistent Store that writes state to a JSON file on disk.
// It keeps an in-memory map as the working set and flushes atomically on every write.
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

func (f *FileStore) ListBySourceName(sourceName string) ([]model.Record, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []model.Record
	for _, r := range f.records {
		if r.SourceName == sourceName {
			out = append(out, r)
		}
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

// flush writes the current in-memory state to disk atomically via a temp file.
func (f *FileStore) flush() error {
	data, err := json.MarshalIndent(f.records, "", "  ")
	if err != nil {
		return err
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path)
}

package registry

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/16bitowl/beacons/internal/model"
)

// FileStore is a persistent Store that writes state to a JSON file on disk.
// It keeps an in-memory map as the working set and flushes atomically on every
// write, unless a Batch is in progress (see Batch).
type FileStore struct {
	mu       sync.RWMutex
	path     string
	records  map[string]model.Record
	batching bool // true inside Batch: writers skip the per-call flush
}

func NewFileStore(path string) (*FileStore, error) {
	fs := &FileStore{path: path, records: make(map[string]model.Record)}
	if err := fs.load(); err != nil {
		return nil, err
	}
	slog.Info("file store initialized", "path", path, "records", len(fs.records))
	return fs, nil
}

func (f *FileStore) Upsert(r model.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[model.RecordKey(r)] = r
	return f.flushLocked()
}

func (f *FileStore) Delete(sourceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, r := range f.records {
		if r.SourceID == sourceID {
			delete(f.records, k)
		}
	}
	return f.flushLocked()
}

func (f *FileStore) DeleteRecord(r model.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.records, model.RecordKey(r))
	return f.flushLocked()
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

// Batch runs fn with the per-call flush suppressed, then flushes once. Since
// FileStore rewrites the whole file per flush, this turns a reconcile pass's
// N record writes into one write instead of N. Not reentrant-safe with
// concurrent callers, but FileStore is only ever mutated by a single
// reconcile/sync goroutine (see internal/reconcile's single-writer
// invariant).
func (f *FileStore) Batch(fn func() error) error {
	f.mu.Lock()
	if f.batching {
		f.mu.Unlock()
		return fn() // nested call; outer Batch owns the deferred flush
	}
	f.batching = true
	f.mu.Unlock()

	fnErr := fn()

	f.mu.Lock()
	f.batching = false
	flushErr := f.flush()
	f.mu.Unlock()

	if fnErr != nil {
		return fnErr
	}
	return flushErr
}

// flushLocked flushes to disk unless a Batch is in progress. Callers must
// hold f.mu.
func (f *FileStore) flushLocked() error {
	if f.batching {
		return nil
	}
	return f.flush()
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

// flush writes the current in-memory state to disk: marshal, write and fsync
// a temp file, rename it over the real path, then fsync the directory so the
// rename itself survives a crash. Callers must hold f.mu.
func (f *FileStore) flush() error {
	data, err := json.MarshalIndent(f.records, "", "  ")
	if err != nil {
		return err
	}

	tmp := f.path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(f.path))
}

// syncDir fsyncs a directory so a preceding file create/rename in it is
// durable across a crash, not just visible in-memory. Linux is the only
// currently supported build target (see Makefile); skipped on platforms
// where directory fsync isn't supported (Windows) so a future build target
// doesn't hard-fail on it.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

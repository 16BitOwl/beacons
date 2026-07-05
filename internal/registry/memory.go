package registry

import (
	"sync"

	"github.com/16bitowl/beacons/internal/model"
)

// MemoryStore is a non-persistent, in-memory Store implementation.
// All records are lost when the process exits.
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
	m.records[model.RecordKey(r)] = r
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

func (m *MemoryStore) DeleteRecord(r model.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.records, model.RecordKey(r))
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

func (m *MemoryStore) ListBySourceName(sourceName string) ([]model.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []model.Record
	for _, r := range m.records {
		if r.SourceName == sourceName {
			out = append(out, r)
		}
	}
	return out, nil
}

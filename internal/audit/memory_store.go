package audit

import (
	"sync"
)

type MemoryStore struct {
	mu      sync.RWMutex
	entries []Entry
	nextID  int64
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1}
}

func (s *MemoryStore) Migrate() error { return nil }

func (s *MemoryStore) Insert(entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.ID = s.nextID
	s.nextID++
	s.entries = append(s.entries, entry)
	return nil
}

func (s *MemoryStore) LastHash() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.entries) == 0 {
		return "", nil
	}
	return s.entries[len(s.entries)-1].EntryHash, nil
}

func (s *MemoryStore) Query(filters QueryFilters) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Entry, len(s.entries))
	copy(result, s.entries)
	return result, nil
}

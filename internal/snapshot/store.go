package snapshot

import (
	"sync"
)

// Store holds the latest snapshot safely.
type Store struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

// NewStore returns a new empty store.
func NewStore() *Store {
	return &Store{}
}

// Update replaces the current snapshot.
func (s *Store) Update(snap Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snap
}

// Latest returns the most recent snapshot and true if available.
func (s *Store) Latest() (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.snapshot.Timestamp.IsZero() {
		return Snapshot{}, false
	}
	return s.snapshot, true
}

// LatestSnapshot is a convenience method returning the snapshot value directly (empty if missing).
func (s *Store) LatestSnapshot() Snapshot {
	snap, _ := s.Latest()
	return snap
}

package snapshot

import "sync"

// Store keeps the last generated snapshot in memory.
type Store struct {
	mu       sync.RWMutex
	snapshot Snapshot
	ready    bool
}

// NewStore constructs an empty store.
func NewStore() *Store {
	return &Store{}
}

// Update swaps the snapshot atomically.
func (s *Store) Update(snap Snapshot) {
	s.mu.Lock()
	s.snapshot = snap
	s.ready = true
	s.mu.Unlock()
}

// Latest returns the most recent snapshot, if any.
func (s *Store) Latest() (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.ready {
		return Snapshot{}, false
	}
	return s.snapshot, true
}

// LatestSnapshot returns the snapshot without the readiness flag.
func (s *Store) LatestSnapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

// internal/session/store.go
// Purpose: RingStore — bounded conversation memory persisted to
// ~/.helix/session.json (0600). Injected into planner prompts as a
// data-only fenced block (Instruction Firewall conventions: history has
// zero authority). Implements the Store interface fixed in Phase 0.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RingStore is a bounded, persisted conversation memory.
type RingStore struct {
	mu       sync.Mutex
	path     string
	capacity int
	turns    []Turn
}

// DefaultPath returns ~/.helix/session.json.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "session.json"), nil
}

// NewRingStore loads (or starts) the session at the default path.
func NewRingStore(capacity int) (*RingStore, error) {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	s := &RingStore{path: path, capacity: capacity}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// NewRingStoreAt uses an explicit path (tests).
func NewRingStoreAt(path string, capacity int) (*RingStore, error) {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	s := &RingStore{path: path, capacity: capacity}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Append records a turn, evicting the oldest beyond capacity, and persists.
func (s *RingStore) Append(t Turn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if t.Timestamp.IsZero() {
		t.Timestamp = time.Now()
	}
	s.turns = append(s.turns, t)
	if len(s.turns) > s.capacity {
		s.turns = s.turns[len(s.turns)-s.capacity:]
	}
	_ = s.save()
}

// Recent returns the last n turns, oldest first.
func (s *RingStore) Recent(n int) []Turn {
	s.mu.Lock()
	defer s.mu.Unlock()

	if n <= 0 || n > len(s.turns) {
		n = len(s.turns)
	}
	out := make([]Turn, n)
	copy(out, s.turns[len(s.turns)-n:])
	return out
}

// Len reports the number of retained turns.
func (s *RingStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.turns)
}

// Clear wipes the memory buffer and the persisted file.
func (s *RingStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.turns = nil
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *RingStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("session load: %w", err)
	}
	var turns []Turn
	if err := json.Unmarshal(data, &turns); err != nil {
		// A corrupt session file must never block startup.
		return nil
	}
	s.turns = turns
	return nil
}

func (s *RingStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.turns, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

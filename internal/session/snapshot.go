// internal/session/snapshot.go
// Purpose: named, on-disk snapshots of the conversation ring so a session can
// be archived and picked back up later (/export, /resume), and so a wipe
// (/clear, /compact) is recoverable instead of destructive.
//
// Snapshots live in ~/.helix/sessions/<id>.json at 0600, same as the live ring:
// they carry the same conversation text and deserve the same protection.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot is one archived conversation.
type Snapshot struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
	Turns     []Turn    `json:"turns"`
}

// SnapshotSummary is the listing row: enough to choose one without loading
// every archived conversation into memory.
type SnapshotSummary struct {
	ID        string
	Label     string
	CreatedAt time.Time
	Turns     int
	Preview   string
	Path      string
}

// SnapshotDir returns ~/.helix/sessions, creating it on demand.
func SnapshotDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".helix", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// SaveSnapshot archives the given turns and returns the snapshot ID. Empty
// input is not an error and writes nothing — callers snapshot defensively
// before a wipe, and an empty conversation has nothing to lose.
func SaveSnapshot(label string, turns []Turn) (string, error) {
	if len(turns) == 0 {
		return "", nil
	}
	dir, err := SnapshotDir()
	if err != nil {
		return "", err
	}
	now := time.Now()
	id := now.Format("20060102-150405")
	snap := Snapshot{ID: id, Label: strings.TrimSpace(label), CreatedAt: now, Turns: turns}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// ListSnapshots returns archived sessions, newest first.
func ListSnapshots() ([]SnapshotSummary, error) {
	dir, err := SnapshotDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []SnapshotSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		snap, err := readSnapshot(path)
		if err != nil {
			// A corrupt archive must not hide the healthy ones.
			continue
		}
		preview := ""
		if len(snap.Turns) > 0 {
			preview = firstLine(snap.Turns[0].UserText)
		}
		out = append(out, SnapshotSummary{
			ID:        snap.ID,
			Label:     snap.Label,
			CreatedAt: snap.CreatedAt,
			Turns:     len(snap.Turns),
			Preview:   preview,
			Path:      path,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// LoadSnapshot reads one archived session by ID.
func LoadSnapshot(id string) (Snapshot, error) {
	dir, err := SnapshotDir()
	if err != nil {
		return Snapshot{}, err
	}
	id = strings.TrimSuffix(filepath.Base(strings.TrimSpace(id)), ".json")
	if id == "" || strings.ContainsAny(id, `/\`) {
		return Snapshot{}, fmt.Errorf("invalid snapshot id")
	}
	return readSnapshot(filepath.Join(dir, id+".json"))
}

// DeleteSnapshot removes one archived session by ID.
func DeleteSnapshot(id string) error {
	dir, err := SnapshotDir()
	if err != nil {
		return err
	}
	id = strings.TrimSuffix(filepath.Base(strings.TrimSpace(id)), ".json")
	if id == "" || strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("invalid snapshot id")
	}
	return os.Remove(filepath.Join(dir, id+".json"))
}

func readSnapshot(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	if snap.ID == "" {
		snap.ID = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	return snap, nil
}

// Restore replaces the ring's contents with the given turns, persisting the
// result. Turns beyond capacity are dropped from the front, so restoring a
// large archive into a small ring keeps the most recent context.
func (s *RingStore) Restore(turns []Turn) error {
	s.mu.Lock()
	if len(turns) > s.capacity {
		turns = turns[len(turns)-s.capacity:]
	}
	s.turns = append([]Turn(nil), turns...)
	err := s.save()
	s.mu.Unlock()
	return err
}

// Capacity reports the ring size, so /context can show headroom rather than
// just a turn count.
func (s *RingStore) Capacity() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capacity
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len([]rune(s)) > 60 {
		return string([]rune(s)[:57]) + "..."
	}
	return s
}

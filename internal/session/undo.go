// internal/session/undo.go
// Purpose: Safe-subset undo journal ("undo that", roadmap 4B). Entries
// record actions with a KNOWN reversal; v1 covers git commits (soft reset)
// — file-write reversals would need pre-write snapshots and are explicitly
// out of scope (documented honestly in the roadmap and BlackBox docs).
// Every reversal re-enters the full safety pipeline + Voice Risk Policy;
// the journal never bypasses them.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UndoEntry is one reversible action.
type UndoEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Description string    `json:"description"`
	Tool        string    `json:"tool"`         // "git" (v1)
	ReversalCmd string    `json:"reversal_cmd"` // executed ONLY through the safety pipeline
}

// UndoJournal is an append-only NDJSON journal at
// ~/.helix/journal/undo.jsonl (0600).
type UndoJournal struct {
	mu   sync.Mutex
	path string
}

// NewUndoJournal opens the journal at the default path.
func NewUndoJournal() (*UndoJournal, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".helix", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &UndoJournal{path: filepath.Join(dir, "undo.jsonl")}, nil
}

// NewUndoJournalAt uses an explicit path (tests).
func NewUndoJournalAt(path string) (*UndoJournal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &UndoJournal{path: path}, nil
}

// Record appends one entry.
func (j *UndoJournal) Record(e UndoEntry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

// Last returns the most recent entry (ok=false when the journal is empty).
func (j *UndoJournal) Last() (UndoEntry, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path)
	if err != nil {
		if os.IsNotExist(err) {
			return UndoEntry{}, false, nil
		}
		return UndoEntry{}, false, err
	}
	lines := splitNonEmpty(data)
	if len(lines) == 0 {
		return UndoEntry{}, false, nil
	}
	var e UndoEntry
	if err := json.Unmarshal(lines[len(lines)-1], &e); err != nil {
		return UndoEntry{}, false, fmt.Errorf("undo journal tail corrupt: %w", err)
	}
	return e, true, nil
}

// GitCommitReversal returns the standard reversal command for the most
// recent commit (soft reset keeps the changes staged).
const GitCommitReversal = "git reset --soft HEAD~1"

func splitNonEmpty(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

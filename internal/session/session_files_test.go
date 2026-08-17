// internal/session/session_files_test.go
// Purpose: RingStore persistence/eviction and the undo journal contract.
package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRingStoreAppendEvictPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	s, err := NewRingStoreAt(path, 3)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	for i := 0; i < 5; i++ {
		s.Append(Turn{UserText: string(rune('a' + i)), Channel: "text"})
	}
	if got := s.Len(); got != 3 {
		t.Fatalf("capacity eviction failed: len=%d, want 3", got)
	}
	recent := s.Recent(2)
	if len(recent) != 2 || recent[0].UserText != "d" || recent[1].UserText != "e" {
		t.Fatalf("Recent order wrong: %+v", recent)
	}

	// Persistence: a fresh store at the same path reloads the ring.
	s2, err := NewRingStoreAt(path, 3)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if s2.Len() != 3 || s2.Recent(1)[0].UserText != "e" {
		t.Fatalf("persisted state wrong: %+v", s2.Recent(10))
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if s.Len() != 0 {
		t.Fatal("clear must empty the buffer")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("clear must remove the persisted file")
	}
}

func TestRingStoreCorruptFileNeverBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewRingStoreAt(path, 5)
	if err != nil {
		t.Fatalf("corrupt session must not block startup: %v", err)
	}
	if s.Len() != 0 {
		t.Fatal("corrupt session must start empty")
	}
}

func TestUndoJournalRecordLast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal", "undo.jsonl")
	j, err := NewUndoJournalAt(path)
	if err != nil {
		t.Fatalf("new journal: %v", err)
	}

	if _, ok, err := j.Last(); err != nil || ok {
		t.Fatalf("empty journal: ok=%v err=%v", ok, err)
	}

	first := UndoEntry{Description: "git commit (a)", Tool: "git", ReversalCmd: GitCommitReversal,
		Timestamp: time.Now().Add(-time.Minute)}
	if err := j.Record(first); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := j.Record(UndoEntry{Description: "git commit (b)", Tool: "git",
		ReversalCmd: GitCommitReversal}); err != nil {
		t.Fatalf("record: %v", err)
	}

	last, ok, err := j.Last()
	if err != nil || !ok {
		t.Fatalf("last: ok=%v err=%v", ok, err)
	}
	if last.Description != "git commit (b)" {
		t.Fatalf("Last must return the newest entry, got %+v", last)
	}
}

// internal/daemon/journal.go
// Purpose: Append-only interaction journal (~/.helix/journal/interactions.jsonl,
// 0600). Records what the daemon did — channel, request text (redacted),
// outcome — for user review and /purge wipeability. Telemetry-free: the
// file never leaves the machine (threat V5).
//
// The writing machinery lives in internal/journal, shared with the opt-in
// voice interaction log (P2.8). This file owns the daemon's SCHEMA; the shared
// package owns permissions, rotation, and redaction. Before that split the
// journal had no rotation at all, despite §7 of the roadmap describing it as
// rotated since the day it was written — an always-on daemon on a small board
// would grow this file without bound.
package daemon

import (
	"encoding/json"
	"path/filepath"
	"time"

	"helix/internal/journal"
)

// JournalEntry is one journalled event.
type JournalEntry struct {
	TS   time.Time `json:"ts"`
	Kind string    `json:"kind"` // submit | wake | connectivity | error | lifecycle
	Chan string    `json:"channel,omitempty"`
	Text string    `json:"text,omitempty"`
	Note string    `json:"note,omitempty"`
}

// Journal is a rotating NDJSON appender over the daemon's schema.
type Journal struct {
	app *journal.Appender
}

// NewJournal opens the default journal.
func NewJournal() (*Journal, error) {
	dir, err := journal.DefaultDir()
	if err != nil {
		return nil, err
	}
	return NewJournalAt(filepath.Join(dir, "interactions.jsonl"))
}

// NewJournalAt uses an explicit path (tests).
func NewJournalAt(path string) (*Journal, error) {
	app, err := journal.Open(path, journal.Options{})
	if err != nil {
		return nil, err
	}
	return &Journal{app: app}, nil
}

// Record appends one entry (best-effort; journaling must never break the daemon).
func (j *Journal) Record(kind, channel, text, note string) {
	if j == nil {
		return
	}
	j.app.Append(JournalEntry{
		TS:   time.Now(),
		Kind: kind,
		Chan: channel,
		Text: journal.Redact(text),
		Note: note,
	})
}

// Tail returns the last n entries (oldest first), for /logs over IPC.
func (j *Journal) Tail(n int) []JournalEntry {
	if j == nil {
		return nil
	}
	lines := j.app.Tail(n)
	out := make([]JournalEntry, 0, len(lines))
	for _, l := range lines {
		var e JournalEntry
		if err := json.Unmarshal(l, &e); err == nil {
			out = append(out, e)
		}
	}
	return out
}

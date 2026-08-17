// internal/daemon/journal.go
// Purpose: Append-only interaction journal (~/.helix/journal/interactions.jsonl,
// 0600). Records what the daemon did — channel, request text (redacted),
// outcome — for user review and /purge wipeability. Telemetry-free: the
// file never leaves the machine (threat V5).
package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// JournalEntry is one journalled event.
type JournalEntry struct {
	TS   time.Time `json:"ts"`
	Kind string    `json:"kind"` // submit | wake | connectivity | error | lifecycle
	Chan string    `json:"channel,omitempty"`
	Text string    `json:"text,omitempty"`
	Note string    `json:"note,omitempty"`
}

// Journal is a mutex-guarded NDJSON appender.
type Journal struct {
	mu   sync.Mutex
	path string
}

// NewJournal opens the default journal.
func NewJournal() (*Journal, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".helix", "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Journal{path: filepath.Join(dir, "interactions.jsonl")}, nil
}

// NewJournalAt uses an explicit path (tests).
func NewJournalAt(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Journal{path: path}, nil
}

// Record appends one entry (best-effort; journaling must never break the daemon).
func (j *Journal) Record(kind, channel, text, note string) {
	if j == nil {
		return
	}
	e := JournalEntry{TS: time.Now(), Kind: kind, Chan: channel, Text: redact(text), Note: note}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// Tail returns the last n entries (oldest first), for /logs over IPC.
func (j *Journal) Tail(n int) []JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := os.ReadFile(j.path)
	if err != nil {
		return nil
	}
	lines := splitLines(data)
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]JournalEntry, 0, len(lines))
	for _, l := range lines {
		var e JournalEntry
		if err := json.Unmarshal(l, &e); err == nil {
			out = append(out, e)
		}
	}
	return out
}

// redact strips control characters and bounds length. It deliberately keeps
// the request visible — the journal exists so the user can audit exactly
// what was asked — while never recording anything beyond the request text.
func redact(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500] + "…"
	}
	return s
}

func splitLines(data []byte) [][]byte {
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

var _ = fmt.Sprintf // keep fmt for future formatting extensions

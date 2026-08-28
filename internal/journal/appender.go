// internal/journal/appender.go
// Purpose: the one append-only NDJSON writer behind every local Helix log —
// the daemon interaction journal (Phase 4) and the opt-in voice interaction
// log (P2.8). Both wanted the same three properties, and the roadmap called
// building them twice waste: 0600 files in a 0700 directory, redaction of
// anything that reaches disk, and size-based rotation so an always-on
// assistant cannot fill a small board's filesystem.
//
// Telemetry-free by construction: this package imports no networking, and a
// test enforces that (threat V5).
package journal

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Rotation defaults. A voice-first assistant writes a line per utterance, so
// the bound that matters is bytes rather than time: 1 MiB of NDJSON is tens of
// thousands of turns, and three generations keeps roughly a week of heavy use
// while staying trivial on a Raspberry Pi's SD card.
const (
	DefaultMaxBytes  int64 = 1 << 20 // rotate past 1 MiB
	DefaultKeepFiles       = 3       // .1, .2, .3 then discard
)

// Appender is a mutex-guarded rotating NDJSON file appender.
//
// Every write is best-effort and errors are deliberately swallowed: a log that
// cannot be written must never break the turn it is describing. Callers that
// need to know whether logging works ask Path and stat it.
type Appender struct {
	mu        sync.Mutex
	path      string
	maxBytes  int64
	keepFiles int
}

// Options configures an Appender. The zero value selects the defaults.
type Options struct {
	MaxBytes  int64
	KeepFiles int
}

// Open prepares an appender at path, creating its directory 0700.
//
// It deliberately does NOT create the file: "default absent" is a privacy
// guarantee for the voice log (threat V5), and a file that appears before
// anything is recorded would break it.
func Open(path string, opts Options) (*Appender, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	a := &Appender{
		path:      path,
		maxBytes:  opts.MaxBytes,
		keepFiles: opts.KeepFiles,
	}
	if a.maxBytes <= 0 {
		a.maxBytes = DefaultMaxBytes
	}
	if a.keepFiles <= 0 {
		a.keepFiles = DefaultKeepFiles
	}
	return a, nil
}

// Path returns the active file path.
func (a *Appender) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// Append marshals v and writes it as one NDJSON line, rotating first if the
// file has outgrown its budget. A nil receiver is a no-op so a disabled log
// needs no nil checks at its call sites.
func (a *Appender) Append(v any) {
	if a == nil {
		return
	}
	line, err := json.Marshal(v)
	if err != nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.rotateIfNeededLocked(int64(len(line) + 1))

	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(line, '\n'))
}

// rotateIfNeededLocked shifts generations when the next write would exceed the
// budget. Rotation happens BEFORE the write rather than after, so the file on
// disk never exceeds maxBytes — checking afterwards would let a single large
// entry sit above the limit until the next append, which on a log that goes
// quiet is indefinitely.
//
// Caller must hold a.mu.
func (a *Appender) rotateIfNeededLocked(incoming int64) {
	info, err := os.Stat(a.path)
	if err != nil || info.Size()+incoming <= a.maxBytes {
		return
	}

	// Drop the oldest generation, then shift the rest down: .2 → .3, .1 → .2,
	// current → .1. Errors are ignored individually; a failed shift costs a
	// generation of history, never the ability to keep logging.
	_ = os.Remove(a.generation(a.keepFiles))
	for i := a.keepFiles - 1; i >= 1; i-- {
		_ = os.Rename(a.generation(i), a.generation(i+1))
	}
	_ = os.Rename(a.path, a.generation(1))
}

// generation returns the rotated filename for generation n (1-based).
func (a *Appender) generation(n int) string {
	return a.path + "." + itoa(n)
}

// itoa avoids importing strconv for single digits while staying correct for
// any generation count a caller might configure.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Tail returns the last n raw lines (oldest first), reading the previous
// generation too so a rotation that just happened does not make recent history
// vanish from a `logs` request.
func (a *Appender) Tail(n int) [][]byte {
	if a == nil || n <= 0 {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	var lines [][]byte
	// Oldest first: read generation 1 (previous) before the active file.
	if data, err := os.ReadFile(a.generation(1)); err == nil {
		lines = append(lines, SplitLines(data)...)
	}
	if data, err := os.ReadFile(a.path); err == nil {
		lines = append(lines, SplitLines(data)...)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// MaxTextBytes bounds one recorded text field. Long enough for any spoken
// utterance or typed request, short enough that a pasted file cannot become
// the log.
const MaxTextBytes = 500

// Redact strips control characters and bounds length.
//
// It deliberately keeps the request VISIBLE: these logs exist so the user can
// audit exactly what was asked and heard, and a redaction that hid the content
// would defeat the audit while still storing it. What it guarantees is that
// nothing on disk can carry terminal escape sequences into a later `cat`, and
// that no single entry is unbounded.
func Redact(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len(s) > MaxTextBytes {
		// Truncate on a rune boundary so a multi-byte character is never cut
		// in half — a severed UTF-8 sequence makes the whole JSON line invalid
		// and silently drops the entry on read-back.
		cut := MaxTextBytes
		for cut > 0 && !isUTF8Start(s[cut]) {
			cut--
		}
		return s[:cut] + "…"
	}
	return s
}

// isUTF8Start reports whether b begins a UTF-8 sequence (ASCII or a leading
// byte), as opposed to a continuation byte.
func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }

// SplitLines splits NDJSON content into non-empty lines.
func SplitLines(data []byte) [][]byte {
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

// DefaultDir returns ~/.helix/journal — the always-on journal's home. The
// voice log lives in its own directory (DefaultVoiceLogDir) so that an opt-in
// speech transcript store and the daemon's operational record can be reasoned
// about, and wiped, separately.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "journal"), nil
}

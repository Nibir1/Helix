// internal/journal/appender_test.go
// Purpose: pin the three properties the shared appender exists to guarantee —
// 0600/0700 permissions, bounded rotation, and redaction that cannot emit a
// broken JSON line — plus the telemetry-free import contract (§9 rule 4).
package journal

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type rec struct {
	N    int    `json:"n"`
	Text string `json:"text"`
}

func TestAppendCreatesFile0600InDir0700(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "voice_log")
	a, err := Open(filepath.Join(dir, "v.jsonl"), Options{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	a.Append(rec{N: 1})

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Fatalf("log directory must be 0700, got %o", perm)
	}
	fi, err := os.Stat(a.Path())
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("log file must be 0600, got %o", perm)
	}
}

// Open must not create the file: "default absent" is a privacy guarantee, and
// a zero-byte file is still a file that says a voice session happened.
func TestOpenDoesNotCreateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice_log", "v.jsonl")
	if _, err := Open(path, Options{}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Open must not create the log file, stat err = %v", err)
	}
}

func TestNilAppenderIsNoOp(t *testing.T) {
	var a *Appender
	a.Append(rec{N: 1}) // must not panic
	if got := a.Path(); got != "" {
		t.Fatalf("nil appender path = %q, want empty", got)
	}
	if got := a.Tail(5); got != nil {
		t.Fatalf("nil appender tail = %v, want nil", got)
	}
}

// Rotation must bound the ACTIVE file, and it must happen before the write
// that would exceed the budget rather than after — otherwise a log that goes
// quiet sits over its limit indefinitely.
func TestRotationBoundsActiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.jsonl")
	a, err := Open(path, Options{MaxBytes: 300, KeepFiles: 2})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 60; i++ {
		a.Append(rec{N: i, Text: strings.Repeat("x", 40)})
		if fi, serr := os.Stat(path); serr == nil && fi.Size() > 300 {
			t.Fatalf("active file grew to %d bytes, budget is 300", fi.Size())
		}
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected a rotated generation .1: %v", err)
	}
	// KeepFiles=2 means .1 and .2 exist and .3 never does.
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("generation .3 must be discarded with KeepFiles=2, err = %v", err)
	}
}

// Tail must read across a rotation boundary, or a `logs` request issued just
// after one would report that nothing had happened.
func TestTailReadsAcrossRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.jsonl")
	a, err := Open(path, Options{MaxBytes: 200, KeepFiles: 3})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for i := 0; i < 20; i++ {
		a.Append(rec{N: i, Text: "entry"})
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("test needs a rotation to have happened: %v", err)
	}

	lines := a.Tail(10)
	if len(lines) != 10 {
		t.Fatalf("tail returned %d lines, want 10", len(lines))
	}
	// Oldest first, and the last entry must be the newest write.
	var last rec
	if err := json.Unmarshal(lines[len(lines)-1], &last); err != nil {
		t.Fatalf("unmarshal last: %v", err)
	}
	if last.N != 19 {
		t.Fatalf("last tail entry n=%d, want 19", last.N)
	}
	var first rec
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("unmarshal first: %v", err)
	}
	if first.N >= last.N {
		t.Fatalf("tail must be oldest-first, got first=%d last=%d", first.N, last.N)
	}
}

func TestRedactStripsControlCharacters(t *testing.T) {
	got := Redact("hello\x1b[31m\x00 world\n")
	if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, 0) {
		t.Fatalf("redact left control characters: %q", got)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Fatalf("redact must keep the content auditable, got %q", got)
	}
}

// A truncation that severs a multi-byte rune produces invalid UTF-8, which
// makes the whole JSON line unparseable — the entry would be silently dropped
// on read-back, so an over-long utterance would vanish from the audit.
func TestRedactTruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("é", MaxTextBytes) // 2 bytes each
	got := Redact(long)
	trimmed := strings.TrimSuffix(got, "…")
	if !utf8Valid(trimmed) {
		t.Fatalf("truncation produced invalid UTF-8: %q", trimmed)
	}

	// And it must survive a real marshal/unmarshal round trip.
	line, err := json.Marshal(rec{Text: got})
	if err != nil {
		t.Fatalf("marshal redacted text: %v", err)
	}
	var back rec
	if err := json.Unmarshal(line, &back); err != nil {
		t.Fatalf("redacted text must round-trip through JSON: %v", err)
	}
	if back.Text != got {
		t.Fatalf("round trip changed the text")
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

// TestNoNetworkImports mirrors the diagnostics contract: a package that writes
// what the user said must never be able to send it anywhere (threat V5).
func TestNoNetworkImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob journal package: %v", err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range af.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "net" || strings.HasPrefix(p, "net/") || p == "crypto/tls" {
				t.Fatalf("journal must be telemetry-free but imports %q", p)
			}
		}
	}
}

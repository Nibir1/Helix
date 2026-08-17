// internal/commands/capture_test.go
// Purpose: BlackBox P8.6 — the output tail is genuinely bounded, keeps the END
// of the stream (where errors live), and tee-ing never swallows or short-writes.
package commands

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestTailBufferKeepsLastBytes(t *testing.T) {
	tb := NewTailBuffer(10)
	if _, err := io.WriteString(tb, "abcdefghijklmnop"); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The tail, not the head: a failing command's diagnosis prints last.
	if got := tb.String(); got != "ghijklmnop" {
		t.Fatalf("tail = %q, want %q", got, "ghijklmnop")
	}
	if !tb.Truncated() {
		t.Fatal("dropping bytes must be reported, or a fragment looks complete")
	}
	if tb.Total() != 16 {
		t.Fatalf("Total() = %d, want 16", tb.Total())
	}
}

func TestTailBufferAccumulatesAcrossWrites(t *testing.T) {
	tb := NewTailBuffer(8)
	for _, s := range []string{"aaa", "bbb", "ccc"} {
		if _, err := io.WriteString(tb, s); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := tb.String(); got != "abbbccc" && got != "aabbbccc" {
		t.Fatalf("unexpected tail %q", got)
	}
	if len(tb.String()) > 8 {
		t.Fatalf("tail exceeded its cap: %d bytes", len(tb.String()))
	}
}

// A short write would make os/exec treat truncation as an I/O error and kill
// the command mid-run — dropping old bytes is intended, not a failure.
func TestTailBufferAlwaysReportsFullWrite(t *testing.T) {
	tb := NewTailBuffer(4)
	n, err := io.WriteString(tb, "this is much longer than four bytes")
	if err != nil {
		t.Fatalf("write returned an error: %v", err)
	}
	if n != len("this is much longer than four bytes") {
		t.Fatalf("Write reported %d bytes, must report the full input", n)
	}
}

func TestTailBufferUnderCapIsExact(t *testing.T) {
	tb := NewTailBuffer(100)
	if _, err := io.WriteString(tb, "short output\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if tb.String() != "short output\n" {
		t.Fatalf("tail = %q, want the exact input", tb.String())
	}
	if tb.Truncated() {
		t.Fatal("output under the cap must not be marked truncated")
	}
}

// exec copies stdout and stderr on separate goroutines, so the buffer must be
// race-free. Run with -race to make this meaningful.
func TestTailBufferConcurrentWrites(t *testing.T) {
	tb := NewTailBuffer(1024)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = io.WriteString(tb, "line of output\n")
			}
		}()
	}
	wg.Wait()
	if len(tb.String()) > 1024 {
		t.Fatalf("cap violated under concurrency: %d bytes", len(tb.String()))
	}
}

func TestOutputCaptureCombinedLabelsBothStreams(t *testing.T) {
	c := NewOutputCapture()
	_, _ = io.WriteString(c.Stdout, "normal line")
	_, _ = io.WriteString(c.Stderr, "error line")

	got := c.Combined()
	if !strings.Contains(got, "[stdout]") || !strings.Contains(got, "[stderr]") {
		t.Fatalf("both streams present must be labelled, got %q", got)
	}

	// A single stream needs no labels — they would be pure noise.
	only := NewOutputCapture()
	_, _ = io.WriteString(only.Stdout, "just stdout")
	if only.Combined() != "just stdout" {
		t.Fatalf("single-stream output must be unlabelled, got %q", only.Combined())
	}

	if NewOutputCapture().Combined() != "" {
		t.Fatal("an empty capture must produce an empty report")
	}
}

func TestNilCaptureIsSafe(t *testing.T) {
	var c *OutputCapture
	if c.Combined() != "" || c.Truncated() {
		t.Fatal("a nil capture must behave as 'no capture', not panic")
	}
}

// The end-to-end guarantee: capture TEES. The terminal must still receive the
// full live output — the user's view is never traded for the planner's.
func TestRunShellCommandCapturedTeesToStdout(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh on this platform: %v", err)
	}

	realStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = realStdout }()

	drained := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		drained <- string(b)
	}()

	ds := NewDirectorySandbox()
	capture := NewOutputCapture()
	dir, _ := os.Getwd()
	if err := ds.RunShellCommandCaptured(
		"echo helix-capture-probe", dir, "sh", nil, capture); err != nil {
		t.Fatalf("run: %v", err)
	}

	_ = w.Close()
	os.Stdout = realStdout
	terminal := <-drained

	if !strings.Contains(terminal, "helix-capture-probe") {
		t.Fatalf("capture swallowed terminal output; terminal saw %q", terminal)
	}
	if !strings.Contains(capture.Combined(), "helix-capture-probe") {
		t.Fatalf("capture missed the output; captured %q", capture.Combined())
	}
}

func TestRunShellCommandCapturedCollectsStderr(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh on this platform: %v", err)
	}

	realStderr := os.Stderr
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("devnull: %v", err)
	}
	os.Stderr = devNull
	defer func() {
		os.Stderr = realStderr
		_ = devNull.Close()
	}()

	ds := NewDirectorySandbox()
	capture := NewOutputCapture()
	dir, _ := os.Getwd()
	// Diagnostics arrive on stderr far more often than stdout — missing that
	// stream would defeat the point of the feature.
	_ = ds.RunShellCommandCaptured("echo boom >&2", dir, "sh", nil, capture)

	if !strings.Contains(capture.Stderr.String(), "boom") {
		t.Fatalf("stderr not captured, got %q", capture.Stderr.String())
	}
}

// The default path must stay byte-identical: a nil capture keeps the child's
// inherited file descriptors, preserving TTY behavior.
func TestNilCapturePreservesDirectExecution(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh on this platform: %v", err)
	}
	ds := NewDirectorySandbox()
	dir, _ := os.Getwd()
	if err := ds.RunShellCommandCaptured("true", dir, "sh", nil, nil); err != nil {
		t.Fatalf("nil-capture execution must behave like RunShellCommand: %v", err)
	}
}

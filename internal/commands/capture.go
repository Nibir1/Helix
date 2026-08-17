// internal/commands/capture.go
//
// Purpose: bounded output capture for the agentic harness (BlackBox P8.6).
//
// The harness could previously see only a step's exit status, so the planner
// knew a command "worked" but never what it printed. `go test` exiting 1 was
// indistinguishable from `git push` exiting 1 — the model had to guess at the
// cause, which is exactly the guessing the observe→replan loop exists to
// eliminate. This file supplies the missing half: a tail of what the command
// actually said.
//
// Two constraints shape the design:
//
//  1. **Bounded, always.** Command output is unbounded (`cat huge.log`,
//     `find /`), and it lands in a planner prompt. A TailBuffer keeps the LAST
//     N bytes in a fixed allocation — the tail, not the head, because errors,
//     summaries, and failure counts print at the end.
//
//  2. **The terminal still gets everything.** Capture tees; it never swallows.
//     The user sees the full, live output exactly as before.
package commands

import (
	"strings"
	"sync"
)

// Capture size limits. These bound prompt growth, not just memory: an
// observation block is re-sent to the planner on every harness iteration, so
// oversized tails cost tokens repeatedly.
const (
	// DefaultCaptureBytes is the per-stream tail kept in memory. Roughly
	// 1k tokens — enough for a compiler error list or a test summary.
	DefaultCaptureBytes = 4096
)

// TailBuffer is an io.Writer that retains only the last max bytes written.
// Safe for concurrent writes: exec copies stdout and stderr on separate
// goroutines, and a shared buffer would otherwise race.
type TailBuffer struct {
	mu    sync.Mutex
	max   int
	buf   []byte
	total int
}

// NewTailBuffer creates a tail buffer retaining the last max bytes
// (max <= 0 → DefaultCaptureBytes).
func NewTailBuffer(max int) *TailBuffer {
	if max <= 0 {
		max = DefaultCaptureBytes
	}
	return &TailBuffer{max: max, buf: make([]byte, 0, max)}
}

// Write appends to the tail, discarding the oldest bytes past the cap. It
// always reports a full write: dropping old output is the intended behavior,
// and returning a short write would make exec treat truncation as an I/O error
// and kill the command mid-run.
func (t *TailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.total += len(p)

	// A single write larger than the cap: keep only its own tail.
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return len(p), nil
	}
	if len(t.buf)+len(p) > t.max {
		drop := len(t.buf) + len(p) - t.max
		t.buf = append(t.buf[:0], t.buf[drop:]...)
	}
	t.buf = append(t.buf, p...)
	return len(p), nil
}

// String returns the retained tail.
func (t *TailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// Truncated reports whether output was dropped, so the report can say so
// rather than presenting a mid-sentence fragment as if it were the whole thing.
func (t *TailBuffer) Truncated() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total > len(t.buf)
}

// Total returns the total number of bytes written, including dropped ones.
func (t *TailBuffer) Total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total
}

// OutputCapture collects a bounded tail of one command's stdout and stderr.
// A nil *OutputCapture means "do not capture" and is the normal, default path:
// callers pass nil unless the agentic harness is running.
type OutputCapture struct {
	Stdout *TailBuffer
	Stderr *TailBuffer

	// ExitCode is the command's real exit status.
	//
	// It lives here because the execution path is deliberately *lenient*:
	// RunShellCommand treats any non-zero exit as success so ordinary
	// interactive results (grep finding nothing, a test suite reporting
	// failures) do not spray errors at the user. That is right for the human
	// and wrong for the planner — under leniency a failing `go test` is
	// indistinguishable from a passing one, so the harness would conclude the
	// goal was met and stop.
	//
	// Recording the code here keeps both: user-facing execution semantics are
	// untouched, while the observation trace regains the truth.
	ExitCode int
}

// NewOutputCapture creates a capture with the default per-stream limit.
func NewOutputCapture() *OutputCapture {
	return &OutputCapture{
		Stdout: NewTailBuffer(DefaultCaptureBytes),
		Stderr: NewTailBuffer(DefaultCaptureBytes),
	}
}

// Combined merges the two tails into one report-ready string, labelling the
// streams only when both carry content (labels on a single stream are noise).
// The returned string is raw; the harness sanitizes it before it reaches a
// prompt.
func (c *OutputCapture) Combined() string {
	if c == nil {
		return ""
	}
	out := strings.TrimSpace(c.Stdout.String())
	errOut := strings.TrimSpace(c.Stderr.String())

	switch {
	case out == "" && errOut == "":
		return ""
	case errOut == "":
		return out
	case out == "":
		return errOut
	default:
		return "[stdout]\n" + out + "\n[stderr]\n" + errOut
	}
}

// Truncated reports whether either stream dropped bytes.
func (c *OutputCapture) Truncated() bool {
	if c == nil {
		return false
	}
	return c.Stdout.Truncated() || c.Stderr.Truncated()
}

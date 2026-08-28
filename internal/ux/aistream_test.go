// internal/ux/aistream_test.go
// Purpose: BlackBox P8.8 — the streaming AI renderer emits its prefix exactly
// once, on first real content, and never leaves an orphaned prefix behind.
package ux

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	real := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()

	_ = w.Close()
	os.Stdout = real
	return <-done
}

func TestAIStreamWriterRendersChunksInOrder(t *testing.T) {
	got := captureStdout(t, func() {
		w := NewUX().StreamAIMessage()
		w.Chunk("Hello")
		w.Chunk(", ")
		w.Chunk("world")
		w.Close()
	})

	if !strings.Contains(got, "Hello, world") {
		t.Fatalf("streamed text lost or reordered: %q", got)
	}
	// The prefix is what marks an AI response; it must appear exactly once,
	// not per chunk.
	if n := strings.Count(got, "[NEURAL_NET]"); n != 1 {
		t.Fatalf("prefix emitted %d times, want exactly 1: %q", n, got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("stream must end the line: %q", got)
	}
}

// Models commonly open with a newline; without trimming, the answer would be
// pushed off the prefix line.
func TestAIStreamWriterTrimsLeadingWhitespace(t *testing.T) {
	got := captureStdout(t, func() {
		w := NewUX().StreamAIMessage()
		w.Chunk("\n\n  ")
		w.Chunk("Answer")
		w.Close()
	})

	idx := strings.Index(got, "[NEURAL_NET]")
	if idx < 0 {
		t.Fatalf("prefix missing: %q", got)
	}
	// No newline may sit between the prefix and the first word.
	between := got[idx:strings.Index(got, "Answer")]
	if strings.Contains(between, "\n") {
		t.Fatalf("leading whitespace was not trimmed: %q", got)
	}
}

// A response that produces nothing must leave no orphaned prefix on screen —
// the reason the prefix is deferred rather than printed up front.
func TestAIStreamWriterEmptyPrintsNothing(t *testing.T) {
	got := captureStdout(t, func() {
		w := NewUX().StreamAIMessage()
		w.Chunk("")
		w.Chunk("   \n ")
		w.Close()
		if w.Started() {
			t.Error("whitespace-only stream must not count as started")
		}
	})

	if got != "" {
		t.Fatalf("an empty stream must print nothing, got %q", got)
	}
}

func TestAIStreamWriterStartedTracksContent(t *testing.T) {
	captureStdout(t, func() {
		w := NewUX().StreamAIMessage()
		if w.Started() {
			t.Error("a fresh writer has not started")
		}
		w.Chunk("x")
		if !w.Started() {
			t.Error("Started must report true once content is rendered")
		}
		w.Close()
	})
}

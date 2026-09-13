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
	// The HEADER is what marks an AI response, and it must appear exactly once,
	// not per chunk. It was "[NEURAL_NET]" until the band layout replaced the
	// prefix with a labelled rule; the guarantee is unchanged and only the
	// marker moved, so this is re-pointed rather than relaxed.
	if n := strings.Count(got, "HELIX"); n != 1 {
		t.Fatalf("band header emitted %d times, want exactly 1: %q", n, got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("stream must end the line: %q", got)
	}
}

// Models commonly open with a newline; without trimming, the answer would start
// one or more blank rail lines below the header.
func TestAIStreamWriterTrimsLeadingWhitespace(t *testing.T) {
	got := captureStdout(t, func() {
		w := NewUX().StreamAIMessage()
		w.Chunk("\n\n  ")
		w.Chunk("Answer")
		w.Close()
	})

	idx := strings.Index(got, "HELIX")
	if idx < 0 {
		t.Fatalf("band header missing: %q", got)
	}
	// Exactly one newline may sit between the header and the first word: the
	// one that ends the header rule itself. Anything more is untrimmed
	// whitespace rendering as empty rail lines.
	between := got[idx:strings.Index(got, "Answer")]
	if n := strings.Count(between, "\n"); n != 1 {
		t.Fatalf("leading whitespace was not trimmed — %d newlines between the header "+
			"and the answer: %q", n, got)
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

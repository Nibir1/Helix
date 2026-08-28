// internal/input/hybrid_test.go
// Purpose: Phase 7 (P7.1) — HybridSource merges typed and voice events into
// one stream while preserving per-event Channel provenance.
package input

import (
	"context"
	"testing"
	"time"
)

func TestHybridSourceMergesStreams(t *testing.T) {
	typed := EventsFrom(
		InputEvent{Text: "ls", Channel: ChannelText},
		InputEvent{Text: "git status", Channel: ChannelText},
	)
	voice := EventsFrom(
		InputEvent{Text: "run the tests", Channel: ChannelVoice},
	)
	h := NewHybridSource(typed, voice)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := h.Events(ctx)
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	got := map[string]bool{}
	for ev := range stream {
		got[ev.Text+"|"+string(ev.Channel)] = true
	}

	for _, want := range []string{
		"ls|text", "git status|text", "run the tests|voice",
	} {
		if !got[want] {
			t.Fatalf("missing event %q in merged stream; got %v", want, got)
		}
	}
}

func TestHybridSourceClose(t *testing.T) {
	h := NewHybridSource(EventsFrom(InputEvent{Text: "x", Channel: ChannelText}))
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// internal/speech/chain_health_test.go
// Purpose: the registry must record which providers failed on the most recent
// chain run, so callers can report degradation without probing. The interactive
// per-turn status line depends on this being a free read.
package speech

import (
	"context"
	"errors"
	"testing"
)

func TestTranscribeRecordsCleanPrimary(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "primary", text: "hello"})
	reg.SetConfig(Config{STT: STTConfig{Provider: "primary"}})

	if h := reg.LastSTTHealth(); h.Attempted {
		t.Error("a registry that has not transcribed must report an unused chain")
	}
	if _, err := reg.Transcribe(context.Background(), AudioFormat{Bytes: []byte{0}}); err != nil {
		t.Fatalf("transcribe: %v", err)
	}

	h := reg.LastSTTHealth()
	if !h.OK || h.Used != "primary" || len(h.Failed) != 0 {
		t.Fatalf("health = %+v, want a clean primary-only run", h)
	}
	if h.Degraded() {
		t.Error("a primary-only success is not degradation")
	}
}

func TestTranscribeRecordsFallback(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "groq", err: errors.New("upstream 500")})
	reg.RegisterSTT(&fakeSTT{name: "whisper-local", text: "heard it", local: true})
	reg.SetConfig(Config{STT: STTConfig{
		Provider: "groq", Fallbacks: []string{"whisper-local"},
	}})

	if _, err := reg.Transcribe(context.Background(), AudioFormat{Bytes: []byte{0}}); err != nil {
		t.Fatalf("transcribe: %v", err)
	}

	h := reg.LastSTTHealth()
	if !h.Degraded() {
		t.Fatalf("health = %+v, want degraded: the primary failed", h)
	}
	if h.Used != "whisper-local" {
		t.Errorf("Used = %q, want the fallback that answered", h.Used)
	}
	if len(h.Failed) != 1 || h.Failed[0] != "groq" {
		t.Errorf("Failed = %v, want [groq]", h.Failed)
	}
	// The reason has to name both halves — which provider is down and which one
	// is carrying the load — since it is all the status line gets to show.
	if r := h.Reason(); r == "" {
		t.Error("a degraded chain must produce a reason")
	}
}

// An empty transcript counts as a provider failure (fallbacks get a chance), so
// it must show up in the health record too.
func TestTranscribeRecordsEmptyTranscriptAsFailure(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "silent"}) // succeeds with empty text
	reg.RegisterSTT(&fakeSTT{name: "backup", text: "got it"})
	reg.SetConfig(Config{STT: STTConfig{Provider: "silent", Fallbacks: []string{"backup"}}})

	if _, err := reg.Transcribe(context.Background(), AudioFormat{Bytes: []byte{0}}); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if h := reg.LastSTTHealth(); len(h.Failed) != 1 || h.Failed[0] != "silent" {
		t.Errorf("Failed = %v, want [silent]", h.Failed)
	}
}

func TestTranscribeRecordsTotalFailure(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "a", err: errors.New("down")})
	reg.RegisterSTT(&fakeSTT{name: "b", err: errors.New("also down")})
	reg.SetConfig(Config{STT: STTConfig{Provider: "a", Fallbacks: []string{"b"}}})

	if _, err := reg.Transcribe(context.Background(), AudioFormat{Bytes: []byte{0}}); err == nil {
		t.Fatal("expected the chain to fail")
	}

	h := reg.LastSTTHealth()
	if h.OK || !h.Degraded() {
		t.Fatalf("health = %+v, want a failed chain", h)
	}
	if len(h.Failed) != 2 {
		t.Errorf("Failed = %v, want both providers", h.Failed)
	}
}

// An unconfigured chain is degradation the moment something tries to use it —
// otherwise "no STT provider" would print CLEAR.
func TestTranscribeRecordsMissingChain(t *testing.T) {
	reg := newTestRegistry(t)
	if _, err := reg.Transcribe(context.Background(), AudioFormat{Bytes: []byte{0}}); err == nil {
		t.Fatal("an empty chain must be an error")
	}
	h := reg.LastSTTHealth()
	if !h.Degraded() {
		t.Fatalf("health = %+v, want degraded", h)
	}
	if h.Reason() != "no provider configured" {
		t.Errorf("reason = %q, want it to name the missing configuration", h.Reason())
	}
}

func TestSynthesizeRecordsChainHealth(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterTTS(&fakeTTS{name: "cloud", err: errors.New("402 payment required")})
	reg.RegisterTTS(&fakeTTS{name: "piper-local", local: true})
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "cloud", Fallbacks: []string{"piper-local"}}})

	if _, err := reg.Synthesize(context.Background(), "hi", SynthesisOptions{}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	h := reg.LastTTSHealth()
	if !h.Degraded() || h.Used != "piper-local" {
		t.Fatalf("health = %+v, want a fallback to piper-local", h)
	}
}

// A cancelled call says nothing about provider health, so it must not overwrite
// the last real outcome — a barge-in should not turn the status line red.
func TestCancelledCallDoesNotOverwriteHealth(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterTTS(&fakeTTS{name: "cloud"})
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "cloud"}})

	if _, err := reg.Synthesize(context.Background(), "hi", SynthesisOptions{}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	before := reg.LastTTSHealth()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.Synthesize(ctx, "interrupted", SynthesisOptions{}); err == nil {
		t.Fatal("a cancelled context must be reported")
	}

	after := reg.LastTTSHealth()
	if after.Used != before.Used || after.OK != before.OK {
		t.Errorf("health changed from %+v to %+v on a barge-in", before, after)
	}
}

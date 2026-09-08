// cmd/helix/tts_metric_provider_test.go
// Purpose: the TTS latency sample must name the provider that actually spoke.
//
// §10 grades TTS first-audio against two budgets — 800 ms cloud, 1500 ms local —
// and `/blackbox stats` picks the column from the provider recorded with the
// sample. activeTTSProvider returned the HEAD of the failover chain, so on a
// failover the wrong column was chosen: a cloud synthesis that missed 800 ms
// was filed under the local primary and reported as meeting a 1500 ms target.
//
// Hermetic: the "fallback" is a real piper-local adapter pointed at an httptest
// server, and the primary is a provider with no key, which is how a chain
// actually degrades (§9 rule 1 — no hardware, no network).
package main

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"

	"helix/internal/config"
	"helix/internal/speech"
)

// wavBytes is the smallest well-formed 16-bit mono WAV Helix's decoder accepts:
// a header plus a few non-silent samples.
func wavBytes() []byte {
	const samples = 400
	data := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(int16((i%40)*400)))
	}
	out := make([]byte, 0, 44+len(data))
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(36+len(data)))
	out = append(out, "WAVEfmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 1)     // PCM
	out = binary.LittleEndian.AppendUint16(out, 1)     // mono
	out = binary.LittleEndian.AppendUint32(out, 22050) // rate
	out = binary.LittleEndian.AppendUint32(out, 22050*2)
	out = binary.LittleEndian.AppendUint16(out, 2)
	out = binary.LittleEndian.AppendUint16(out, 16)
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	return append(out, data...)
}

func TestTTSMetricNamesTheProviderThatSpoke(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wavBytes())
	}))
	t.Cleanup(srv.Close)

	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg = &config.Config{}

	// elevenlabs first — a cloud provider with no key in this environment, so
	// the chain must walk past it — then the local sidecar, which answers.
	cfg.Speech.TTS.Provider = "elevenlabs"
	cfg.Speech.TTS.Fallbacks = []string{"piper-local"}
	cfg.Speech.TTS.Endpoints = map[string]string{"piper-local": srv.URL}

	if err := speech.Init(cfg.Speech.Runtime()); err != nil {
		t.Skipf("speech engine unavailable here: %v", err)
	}
	reg := speech.Default()
	if reg == nil {
		t.Skip("no speech registry")
	}
	if err := speech.SaveTTSKey("elevenlabs", ""); err != nil {
		t.Skipf("cannot clear a key in this environment: %v", err)
	}

	// The precondition (§9 rule 8): the primary must really be ahead of the
	// fallback, or "names the provider that spoke" and "names the head" agree
	// and the test proves nothing.
	chain := reg.TTSChain()
	if len(chain) < 2 || chain[0] == "piper-local" {
		t.Fatalf("chain = %v, want elevenlabs ahead of piper-local", chain)
	}

	if _, err := speech.Synthesize(context.Background(), "hello"); err != nil {
		t.Skipf("neither provider answered in this environment: %v", err)
	}

	h := reg.LastTTSHealth()
	if !h.OK || h.Used != "piper-local" {
		t.Fatalf("expected the fallback to answer, got Used=%q OK=%v failed=%v",
			h.Used, h.OK, h.Failed)
	}
	if got := activeTTSProvider(); got != "piper-local" {
		t.Errorf("activeTTSProvider() = %q, want piper-local — the sample would be graded "+
			"against the wrong §10 column", got)
	}
}

// Before anything has been spoken there is no answer but the head, and that is
// the right one: the chain's primary is what the next reply will try.
func TestTTSMetricFallsBackToTheChainHeadBeforeFirstUse(t *testing.T) {
	saved := cfg
	t.Cleanup(func() { cfg = saved })
	cfg = &config.Config{}
	cfg.Speech.TTS.Provider = "piper-local"

	if err := speech.Init(cfg.Speech.Runtime()); err != nil {
		t.Skipf("speech engine unavailable here: %v", err)
	}
	if speech.Default() == nil {
		t.Skip("no speech registry")
	}
	if got := activeTTSProvider(); got != "piper-local" {
		t.Errorf("activeTTSProvider() = %q, want the chain head piper-local", got)
	}
}

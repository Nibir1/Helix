//go:build !windows

// tests/e2e/speech_e2e_test.go
// Purpose: BlackBox Phase 1 e2e — /say drives the real binary's TTS chain
// into the in-process mock (zero real network, zero real keys). Playback
// itself is device-dependent and not asserted; synthesis reaching the mock
// and the [voice] transcript line are.
package e2e

import (
	"testing"
	"time"
)

func TestE2E_SayHitsMockTTS(t *testing.T) {
	t.Setenv("HELIX_E2E_SPEECH", "1")
	h := newHarness(t, unusedPlan)
	defer h.Close()

	if got := h.TTSHits(); got != 0 {
		t.Fatalf("precondition: mock TTS already hit %d times", got)
	}

	h.WriteLine("/blackbox say voice link online")
	if err := h.Expect("[voice] voice link online", 20*time.Second); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if h.TTSHits() >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("/blackbox say never reached the mock TTS endpoint (hits=%d)\n----- captured output -----\n%s",
		h.TTSHits(), h.stripped())
}

func TestE2E_TTSToggle(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	h.WriteLine("/blackbox tts off")
	if err := h.Expect("Automatic spoken responses disabled", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	h.WriteLine("/blackbox tts on")
	if err := h.SendExpect("/blackbox tts on", "Automatic spoken responses enabled", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

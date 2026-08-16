//go:build !windows

// tests/e2e/voice_mode_e2e_test.go
// Purpose: BlackBox Phase 2 e2e — mode-switch commands and the safety valve
// without audio hardware. Entering voice mode is machine-dependent (needs a
// recorder + STT), so the e2e proves the text-mode side of the switch and
// that the REPL loop survives mode churn. Voice-path behavior is proven by
// the synthetic-injection unit tests in internal/agent.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestE2E_ManualModeSafetyValve(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	// /manual in text mode is an informative no-op...
	h.WriteLine("/manual")
	if err := h.Expect("Already in text mode", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// ...and /voice off always restores a working typed loop.
	h.WriteLine("/voice off")
	if err := h.Expect("TEXT MODE", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// The REPL must still execute after mode churn.
	if err := os.WriteFile(filepath.Join(h.project, "voice_mode_probe.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.WriteLine("ls voice_mode_probe.txt")
	if err := h.Expect("GRID STATUS", 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.Expect("voice_mode_probe.txt", 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_VoiceRefusedWithoutSTT(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	// No STT provider is configured in the default harness, so /voice must
	// refuse entry and keep the shell in text mode — the mic-less-machine
	// guarantee (roadmap P2.2). With a recorder present the refusal names
	// /voice-setup; without one it names the recorder install hint.
	h.WriteLine("/voice")
	if err := h.Expect("voice mode", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	out := h.stripped()
	if !strings.Contains(out, "voice-setup") && !strings.Contains(out, "recorder") {
		t.Fatalf("refusal must explain why voice mode was refused; got: %s", out)
	}

	h.WriteLine("echo mode_refused_check")
	if err := h.Expect("GRID STATUS", 15*time.Second); err != nil {
		t.Fatalf("text loop must remain fully functional after refused /voice: %v", err)
	}
}

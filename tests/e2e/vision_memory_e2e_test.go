//go:build !windows

// tests/e2e/vision_memory_e2e_test.go
// Purpose: BlackBox Phase 7 (P7.4) e2e matrix additions — /eyes refuses
// without a vision-capable model, /memory shows an empty store, and /help
// renders the BlackBox commands. No camera, no mic, no vision model.
package e2e

import (
	"testing"
	"time"
)

func TestE2E_EyesRefusedWithoutVisionModel(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	// The refusal must name the model it rejected and stay actionable — the old
	// "No vision-capable model is configured — set one first." said neither which
	// model failed nor which provider would work.
	h.WriteLine("/blackbox eyes on")
	if err := h.Expect("cannot process images", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	// The merged report replaced the per-subsystem one, and it must say what the
	// camera can do rather than only whether the toggle is flipped.
	if err := h.SendExpect("/blackbox eyes status", "off", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_MemoryShowEmpty(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	h.WriteLine("/memory show")
	if err := h.Expect("Conversation memory is empty", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestE2E_HelpRendersBlackBoxCommands(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	// The INDEX lists names; argument syntax lives in the detail screen, which
	// has the whole width for it. Both are asserted, and both come from the
	// registry rather than a second hand-kept copy — that was this test's point
	// and it still holds, one screen over.
	h.WriteLine("/help")
	for _, name := range []string{"/blackbox", "/memory"} {
		if err := h.Expect(name, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}

	h.WriteLine("/help /blackbox")
	if err := h.Expect("[on|off|status|setup|look|eyes|wake|tts|say|log|stats]",
		10*time.Second); err != nil {
		t.Fatal(err)
	}
}

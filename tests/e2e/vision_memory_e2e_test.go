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
	h.WriteLine("/eyes on")
	if err := h.Expect("cannot process images", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	h.WriteLine("/eyes status")
	if err := h.SendExpect("/eyes status", "Eyes: off", 10*time.Second); err != nil {
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

	h.WriteLine("/help")
	// Usage strings come from the command registry, so assert on the usage
	// lines the registry actually publishes rather than a second hand-kept copy.
	for _, cmd := range []string{"/eyes [on|off|status|look]", "/memory [show|clear]"} {
		if err := h.Expect(cmd, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}
}

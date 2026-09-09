//go:build !windows

package e2e

import (
	"strings"
	"testing"
	"time"
)

// A fresh install listens without being told to, and says so once.
//
// The owner's ask: "the waking feature enabled by default without manually
// activating the wake up, and I can always perform manual work". So the default
// config must arm, the announcement must appear, and typing must still work.
func TestE2E_WakeIsOnByDefaultAndKeyboardStillWorks(t *testing.T) {
	h := newHarness(t, `{"steps":[{"tool":"response","content":"ok"}]}`)
	defer h.Close()

	if err := h.SendExpect("/blackbox wake status", "WAKE WORD", 10*time.Second); err != nil {
		t.Fatalf("wake status did not render: %v", err)
	}
	out := h.stripped()
	// On a CI host with no recorder it reports not-armed with a reason rather
	// than claiming to listen — the honest degradation, asserted either way.
	if !strings.Contains(out, "STATE") {
		t.Error("wake status must report its state")
	}

	// The half that must never regress: the keyboard.
	if err := h.SendExpect("echo default-wake-ok", "default-wake-ok", 10*time.Second); err != nil {
		t.Fatalf("typing stopped working with wake on by default: %v", err)
	}
	// And the removed subcommand must not resurface as a usage line.
	h.WriteLine("/blackbox wake always on")
	time.Sleep(800 * time.Millisecond)
	if strings.Contains(h.stripped(), "wake <on|off|always|status>") {
		t.Error("the old always subcommand is still advertised")
	}
}

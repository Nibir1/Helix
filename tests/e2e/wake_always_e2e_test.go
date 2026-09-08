//go:build !windows

// tests/e2e/wake_always_e2e_test.go
// Purpose: prove always-listen changed nothing for everyone else.
//
// The feature adds a branch in front of the manual prompt's blocking read. The
// guardrail it has to respect is the one Phase 4A was accepted on — the
// interactive loop behaves identically — and the only honest way to check that
// is the real binary under a PTY, because the branch is about terminal modes
// and a unit test cannot see a terminal mode.
package e2e

import (
	"strings"
	"testing"
	"time"
)

// With always-listen unset (the default), the prompt is the blocking read it
// has always been: no arming line, no HUD, and typed commands work.
func TestE2E_UnarmedPromptIsUnchanged(t *testing.T) {
	h := newHarness(t, `{"steps":[{"tool":"response","content":"ok"}]}`)
	defer h.Close()

	if err := h.SendExpect("/blackbox wake status", "WAKE WORD", 10*time.Second); err != nil {
		t.Fatalf("wake status did not render: %v", err)
	}
	out := h.stripped()
	if strings.Contains(out, "say the wake word to go live") {
		t.Error("an unconfigured Helix printed the armed-prompt line — the microphone must " +
			"not be held open at a prompt nobody asked to arm")
	}
	if !strings.Contains(out, "AT THE PROMPT") {
		t.Error("wake status should report where the wake word is heard")
	}

	// The shell still works, which is the half a terminal-mode bug would break.
	if err := h.SendExpect("echo unarmed-ok", "unarmed-ok", 10*time.Second); err != nil {
		t.Fatalf("typed command after the status report failed: %v", err)
	}
}

// Turning it on without a recorder must not strand the session: it reports what
// is missing and the keyboard keeps working. This is the mic-less machine case,
// which is every CI runner.
func TestE2E_AlwaysListenWithoutARecorderKeepsTheKeyboard(t *testing.T) {
	h := newHarness(t, `{"steps":[{"tool":"response","content":"ok"}]}`)
	defer h.Close()

	h.WriteLine("/blackbox wake always on")
	time.Sleep(1500 * time.Millisecond)

	// Whatever it decided, the shell must still take a typed line afterwards.
	if err := h.SendExpect("echo still-here", "still-here", 10*time.Second); err != nil {
		t.Fatalf("the prompt stopped accepting input after arming was requested: %v", err)
	}

	out := h.stripped()
	if strings.Contains(out, "panic") {
		t.Fatalf("arming panicked:\n%s", out)
	}
}

//go:build !windows

// tests/e2e/wake_arms_e2e_test.go
// Purpose: prove, against the real binary under a real pty, that a fresh
// install actually arms the keyboard prompt — and that a config carrying an
// explicit `"enabled": false` does not.
//
// WHY THIS EXISTS SEPARATELY from TestE2E_WakeIsOnByDefaultAndKeyboardStillWorks:
// that test asserts the status panel RENDERS, because a CI host has no
// recorder and could not arm whatever the config said. It therefore passed
// while the default was inert, which is the whole failure this file answers.
// Here the conditions are checked first and the test SKIPS when they are
// absent, so it is a real assertion on a developer machine and never a false
// green anywhere else.
//
// What it still cannot do is speak. No test in this repo makes a sound (§9
// rule 1); "a sound enters live mode" stays manual QA. What is mechanised is
// the decision that precedes it — whether the microphone is open at all —
// which is exactly the part that was broken and invisible.
package e2e

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// requireRecorder skips unless this host can actually arm a prompt.
func requireRecorder(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rec"); err != nil {
		if _, err := exec.LookPath("sox"); err != nil {
			t.Skip("no sox/rec on this host: arming is correctly impossible, nothing to assert")
		}
	}
}

// A fresh install — no wake_word section at all — listens at the prompt.
//
// The announcement is the assertion rather than the status panel, because it is
// printed by the armed wait itself: seeing it means the microphone is open and
// the poll loop is running, not merely that a config field reads true.
func TestE2E_FreshInstallArmsTheKeyboardPrompt(t *testing.T) {
	requireRecorder(t)
	t.Setenv("HELIX_E2E_STT", "1")

	h := newHarness(t, `{"steps":[{"tool":"response","content":"ok"}]}`)
	defer h.Close()

	if err := h.Expect("make any sound to go live", 20*time.Second); err != nil {
		t.Fatalf("a fresh install did not arm the prompt: %v\n--- output ---\n%s",
			err, tailOf(h.stripped(), 2000))
	}
	if err := h.SendExpect("echo armed-and-typing", "armed-and-typing", 20*time.Second); err != nil {
		t.Fatalf("the keyboard stopped working while armed: %v", err)
	}
}

// The upgrade case, and the reason "on by default" is not the whole story.
//
// Older builds stored these as plain bools, which are ALWAYS marshalled, so
// every config that build saved holds a literal `"enabled": false` nobody
// chose. The new default reaches an ABSENT key and must not override an
// explicit one: `enabled: false` is the opt-out docs/blackbox.md tells people
// to write, so resurrecting it would open a microphone on a guess — the single
// direction ADR-005 forbids. This pins that, because the tempting "migrate the
// stale false" fix would silently violate it.
func TestE2E_ExplicitFalseIsNeverOverriddenByTheDefault(t *testing.T) {
	requireRecorder(t)
	t.Setenv("HELIX_E2E_STT", "1")
	// The owner's config verbatim, as written by the previous build.
	t.Setenv("HELIX_E2E_WAKE_JSON", `{"enabled": false, "engine": "", "phrase": "",
		"sensitivity_preset": "", "cooldown_s": 0, "chunk_ms": 0}`)

	h := newHarness(t, `{"steps":[{"tool":"response","content":"ok"}]}`)
	defer h.Close()

	// Give it at least as long as the armed case needed to print.
	if err := h.SendExpect("echo not-armed-check", "not-armed-check", 20*time.Second); err != nil {
		t.Fatalf("shell unusable: %v", err)
	}
	if strings.Contains(h.stripped(), "make any sound to go live") {
		t.Error("a config saying enabled:false armed the prompt — the default must not " +
			"override an explicit opt-out")
	}

	// And it must SAY so, with the way back, or the user is left guessing why
	// nothing listens. This is the sentence the owner needed and did not get.
	if err := h.SendExpect("/blackbox wake status", "WAKE WORD", 20*time.Second); err != nil {
		t.Fatalf("wake status did not render: %v", err)
	}
	out := h.stripped()
	if !strings.Contains(out, "/blackbox wake on") {
		t.Errorf("an off state must name the command that turns it on\n--- output ---\n%s",
			tailOf(out, 1500))
	}
}

// tailOf keeps failure output readable: the interesting part of a pty capture
// is always the end.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

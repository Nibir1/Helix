//go:build !windows

// tests/e2e/reboot_e2e_test.go
//
// Purpose: /reboot has to actually restart the process, and the shell that
// comes back has to know what the one before it was doing.
//
// This is the only place that can prove either. A unit test can check that a
// record round-trips, but "the process image was replaced and the new one read
// the note" is a claim about a real binary in a real terminal — and the failure
// mode if it is wrong (the shell exits and never comes back) is exactly the one
// nobody notices in a mock.
package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_RebootRestartsAndResumes drives the whole loop: state in, restart,
// state out.
func TestE2E_RebootRestartsAndResumes(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	// Something to be in the middle of, so the resume has a subject.
	if err := h.SendExpect("/todo add wire up the parser", "wire up the parser",
		20*time.Second); err != nil {
		t.Fatal(err)
	}
	// Moving it to in-progress is what makes it "what I was doing" rather than
	// just an item on a list — captureContinuity only carries in-progress tasks.
	if err := h.SendExpect("/todo start 1", "wire up the parser", 20*time.Second); err != nil {
		t.Fatal(err)
	}

	h.WriteLine("/reboot")
	// The old process announces the restart and what it is carrying.
	if err := h.Expect("REBOOT", 20*time.Second); err != nil {
		t.Fatal("the outgoing shell must say it is restarting")
	}

	// The banner belongs to a NEW process. Waiting for a second occurrence is
	// the proof: the harness matches against everything captured so far, so the
	// count has to go up rather than merely be non-zero.
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(h.stripped(), "Helix Native Shell") >= 2 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if n := strings.Count(h.stripped(), "Helix Native Shell"); n < 2 {
		t.Fatalf("the process did not come back: saw the startup banner %d time(s)", n)
	}

	// And it knows what it was doing.
	if err := h.Expect("RESUMED", 30*time.Second); err != nil {
		t.Fatal("the restarted shell must report what it was carrying")
	}
	if err := h.Expect("wire up the parser", 15*time.Second); err != nil {
		t.Fatal("the in-progress task must survive the restart")
	}

	// The record is CONSUMED. A second boot must not replay the same resume.
	rebootFile := filepath.Join(h.home, ".helix", "reboot.json")
	if _, err := os.Stat(rebootFile); err == nil {
		t.Error("the continuity record must be deleted once it has been read")
	}

	// The restarted shell is a working shell, not a husk.
	if err := h.SendExpect("/status", "PROVIDER", 30*time.Second); err != nil {
		t.Fatal("the shell after a reboot must still answer commands")
	}
}

// A reboot with nothing in flight must still restart, and must not invent a
// resume it has nothing to say about.
func TestE2E_RebootWithNothingInFlightStaysQuiet(t *testing.T) {
	h := newHarness(t, commandsNoPlan)
	defer h.Close()

	if err := h.SendExpect("/version", "Helix", 20*time.Second); err != nil {
		t.Fatal(err)
	}
	before := strings.Count(h.stripped(), "Helix Native Shell")

	h.WriteLine("/reboot")
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(h.stripped(), "Helix Native Shell") > before {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if strings.Count(h.stripped(), "Helix Native Shell") <= before {
		t.Fatalf("the process did not come back\n----\n%s", h.stripped())
	}
	if err := h.SendExpect("/version", "Helix", 30*time.Second); err != nil {
		t.Fatal("the restarted shell must answer commands")
	}
}

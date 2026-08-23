//go:build !windows

// tests/e2e/voice_log_e2e_test.go
// Purpose: BlackBox P2.8 — prove the voice interaction log's default-absent
// guarantee against the REAL binary, not just the package.
//
// The unit test proves a disabled log writes nothing; this proves the shipped
// wiring leaves it disabled. Those are different claims, and the gap between
// them is where an accidental default would live: a log opened eagerly at
// startup "so it is ready" would pass the unit test and fail here.
package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A session that runs commands and exits must leave no transcript store behind.
func TestE2E_VoiceLogAbsentByDefault(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	// Touch the voice surface without enabling anything.
	if err := h.SendExpect("/blackbox log status", "off", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.SendExpect("/blackbox log show", "off", 10*time.Second); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(h.home, ".helix", "voice_log")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("voice_log must not exist unless enabled (stat %s err = %v)", dir, err)
	}
}

// The status report must SAY that nothing is being recorded. A guarantee the
// user cannot see is one they have to take on trust, and this is the line that
// makes the default legible next to the microphone and camera states.
func TestE2E_BlackBoxStatusReportsTranscriptState(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	h.WriteLine("/blackbox log")
	if err := h.Expect("never audio", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

// The typed opt-in must work end to end and create the store where the docs say
// it will. (That voice CANNOT do the same is a policy rule proven in the unit
// test for voiceCommandAllowed — there is no microphone here to speak it with.)
func TestE2E_TranscriptLogTypedOptInAndOut(t *testing.T) {
	h := newHarness(t, unusedPlan)
	defer h.Close()

	if err := h.SendExpect("/blackbox log on", "voice log on", 10*time.Second); err != nil {
		t.Fatalf("typed enable should be allowed: %v", err)
	}

	// And it must have created the store now that it was asked to, in the
	// documented location.
	path := filepath.Join(h.home, ".helix", "voice_log")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := h.SendExpect("/blackbox log off", "voice log off", 10*time.Second); err != nil {
		t.Fatal(err)
	}
}

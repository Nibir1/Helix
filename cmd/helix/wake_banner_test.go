// cmd/helix/wake_banner_test.go
// Purpose: the wake banners must describe the engine that actually runs.
// /wake on used to promise `after each turn I listen for "hey helix"` regardless
// of engine, while the default `energy` detector scores RMS and wakes on any
// sound — it has no phrase matching at all.
package main

import (
	"strings"
	"testing"
)

func TestWakeBannerEnergyEngineDoesNotPromisePhraseDetection(t *testing.T) {
	banner := strings.Join(wakeBannerLines("energy", "hey helix"), "\n")

	if !strings.Contains(banner, "ANY speech") {
		t.Errorf("the energy engine wakes on any speech and must say so:\n%s", banner)
	}
	if !strings.Contains(banner, "sidecar") {
		t.Errorf("the banner should name the engine that CAN spot a phrase:\n%s", banner)
	}
	// The exact false promise: "listen for \"hey helix\"".
	if strings.Contains(banner, `listen for "hey helix"`) {
		t.Errorf("the energy banner must not promise phrase spotting:\n%s", banner)
	}
	// A blank engine also runs energy — the default must be covered too.
	if blank := strings.Join(wakeBannerLines("", "hey helix"), "\n"); !strings.Contains(blank, "ANY speech") {
		t.Errorf("an unset engine defaults to energy and must be worded the same:\n%s", blank)
	}
}

func TestWakeBannerSidecarEngineKeepsThePhrasePromise(t *testing.T) {
	banner := strings.Join(wakeBannerLines("sidecar", "hey helix"), "\n")

	// The phrase must appear — the sidecar engine really does match it. The
	// wording moved when arming became the default: the banner now leads with
	// what is listening RIGHT NOW ("say \"hey helix\" and I go live") instead of
	// "after each turn I listen for …", which presupposed turns and left a user
	// at the keyboard asking how to wake it. So this asserts the promise, not
	// the sentence it used to live in.
	if !strings.Contains(banner, `"hey helix"`) {
		t.Errorf("the sidecar engine really does match the phrase:\n%s", banner)
	}
	if strings.Contains(banner, "ANY speech") {
		t.Errorf("the sidecar banner must not disclaim phrase matching:\n%s", banner)
	}
}

// Both banners must say that waking is ONCE, not per turn, and must name a way
// out.
//
// This replaces an assertion that the banner explained between-turn gating.
// That sentence was true of the old per-turn hold and is now the opposite of
// the truth — a banner is the most authoritative place in the shell to be
// wrong, so the assertion had to move with the behaviour rather than be
// deleted.
func TestWakeBannerSaysWakingIsOncePerConversation(t *testing.T) {
	for _, engine := range []string{"energy", "sidecar"} {
		banner := strings.Join(wakeBannerLines(engine, "hey helix"), "\n")
		if !strings.Contains(banner, "no waking in between") {
			t.Errorf("engine %q banner must say waking is once, not per turn:\n%s", engine, banner)
		}
		if !strings.Contains(banner, "manual mode") {
			t.Errorf("engine %q banner must name a way out:\n%s", engine, banner)
		}
	}
}

func TestWakeBannerFallsBackToTheDefaultPhrase(t *testing.T) {
	banner := strings.Join(wakeBannerLines("sidecar", ""), "\n")
	if !strings.Contains(banner, "hey helix") {
		t.Errorf("a blank phrase must render the default:\n%s", banner)
	}
}

func TestVoiceModeWakeNotes(t *testing.T) {
	if notes := voiceModeWakeNotes(false, "energy", true); notes != nil {
		t.Errorf("with wake off there is nothing to clarify, got %q", notes)
	}

	on := strings.Join(voiceModeWakeNotes(true, "energy", true), "\n")
	if !strings.Contains(on, "no waking in between") {
		t.Errorf("the panel must say every turn runs without re-waking:\n%s", on)
	}
	if !strings.Contains(on, "manual mode") {
		t.Errorf("the panel must name the exit that closes the microphone:\n%s", on)
	}
	if !strings.Contains(on, "stand down") {
		t.Errorf("an open transcribing mic that ends itself must say so:\n%s", on)
	}
	if !strings.Contains(on, "any speech") {
		t.Errorf("the energy engine's behavior belongs here too:\n%s", on)
	}

	// The collapsed form still has to carry the way out, or a re-entry leaves
	// the user in a conversation with no stated exit.
	brief := strings.Join(voiceModeWakeNotes(true, "energy", false), "\n")
	if !strings.Contains(brief, "manual mode") {
		t.Errorf("the collapsed note must still name the exit:\n%s", brief)
	}

	sidecar := strings.Join(voiceModeWakeNotes(true, "sidecar", true), "\n")
	if strings.Contains(sidecar, "any speech") {
		t.Errorf("the sidecar engine does match a phrase:\n%s", sidecar)
	}
}

// The once-per-session discipline for lapse notices.
//
// It used to be a map keyed by a wakeOutcome, because the per-turn wake hold
// was re-entered after every turn and an un-suppressed notice would have
// buried the shell. That hold is gone; the two surviving lapses each carry
// their own flag, and this asserts the one a user can actually hit twice.
func TestArmingLapseIsAnnouncedOncePerSession(t *testing.T) {
	t.Cleanup(func() { armingLapseAnnounced = false })

	armingLapseAnnounced = false
	noteArmingLapse()
	if !armingLapseAnnounced {
		t.Fatal("the first lapse must be announced")
	}
	// A second call is a no-op: the flag is what proves it, since the notice
	// itself goes to the screen.
	noteArmingLapse()
	if !armingLapseAnnounced {
		t.Error("the flag was cleared by a repeat call")
	}
}

// A scanner that dies mid-wait re-arms the announcement, because the "◉
// listening" line was printed once and has to be retracted and re-earned.
func TestScannerDeathReEarnsTheListeningLine(t *testing.T) {
	t.Cleanup(func() { armedPromptAnnounced = false })

	armedPromptAnnounced = true
	noteArmingDied(nil)
	if armedPromptAnnounced {
		t.Error("a dead scanner left the listening line claiming an open microphone")
	}
}

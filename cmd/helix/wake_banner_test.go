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

	if !strings.Contains(banner, `listen for "hey helix"`) {
		t.Errorf("the sidecar engine really does match the phrase:\n%s", banner)
	}
	if strings.Contains(banner, "ANY speech") {
		t.Errorf("the sidecar banner must not disclaim phrase matching:\n%s", banner)
	}
}

// Both banners must say that wake gating applies BETWEEN turns — the other half
// of the QA confusion, since the first turn after /voice on needs no wake.
func TestWakeBannerExplainsBetweenTurnGating(t *testing.T) {
	for _, engine := range []string{"energy", "sidecar"} {
		banner := strings.Join(wakeBannerLines(engine, "hey helix"), "\n")
		if !strings.Contains(banner, "AFTER this one") {
			t.Errorf("engine %q banner must explain when the wake word applies:\n%s", engine, banner)
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
	if notes := voiceModeWakeNotes(false, "energy"); notes != nil {
		t.Errorf("with wake off there is nothing to clarify, got %q", notes)
	}

	on := strings.Join(voiceModeWakeNotes(true, "energy"), "\n")
	if !strings.Contains(on, "BETWEEN turns") {
		t.Errorf("/voice on must say wake gating sits between turns:\n%s", on)
	}
	if !strings.Contains(on, "no wake needed") {
		t.Errorf("/voice on must say the first turn needs no wake word:\n%s", on)
	}
	if !strings.Contains(on, "any speech") {
		t.Errorf("the energy engine's behavior belongs here too:\n%s", on)
	}

	sidecar := strings.Join(voiceModeWakeNotes(true, "sidecar"), "\n")
	if strings.Contains(sidecar, "any speech") {
		t.Errorf("the sidecar engine does match a phrase:\n%s", sidecar)
	}
}

// Wake gating lapsing back to open capture must be announced — and only for the
// causes where something actually changed.
func TestWakeLapseNotice(t *testing.T) {
	cases := []struct {
		outcome  wakeOutcome
		announce bool
		mentions string
	}{
		{wakeWindowExpired, true, "wake window expired"},
		{wakeScannerFailed, true, "recorder unavailable"},
		// Wake was never configured, so nothing lapsed and nothing is said.
		{wakeNotEngaged, false, ""},
		{wakeFired, false, ""},
	}
	for _, tc := range cases {
		got := wakeLapseNotice(tc.outcome)
		if tc.announce {
			if got == "" {
				t.Errorf("outcome %v must produce a notice", tc.outcome)
				continue
			}
			if !strings.Contains(got, tc.mentions) {
				t.Errorf("notice %q should mention %q", got, tc.mentions)
			}
			if !strings.Contains(got, "/blackbox status") {
				t.Errorf("notice %q should point at /blackbox status", got)
			}
		} else if got != "" {
			t.Errorf("outcome %v must stay silent, got %q", tc.outcome, got)
		}
	}
}

// The notice explains a state change; the idle window expires every 60s of
// quiet, so repeating it would bury the shell.
func TestNoteWakeLapseIsOncePerCause(t *testing.T) {
	t.Cleanup(func() { wakeLapseAnnounced = map[wakeOutcome]bool{} })
	wakeLapseAnnounced = map[wakeOutcome]bool{}

	noteWakeLapse(wakeWindowExpired)
	if !wakeLapseAnnounced[wakeWindowExpired] {
		t.Fatal("the first lapse must be announced")
	}
	// A second call is a no-op; a different cause still gets its own notice.
	noteWakeLapse(wakeWindowExpired)
	noteWakeLapse(wakeScannerFailed)
	if !wakeLapseAnnounced[wakeScannerFailed] {
		t.Error("a different cause deserves its own one-time notice")
	}
	noteWakeLapse(wakeNotEngaged)
	if wakeLapseAnnounced[wakeNotEngaged] {
		t.Error("a cause with no notice must not be marked announced")
	}
}

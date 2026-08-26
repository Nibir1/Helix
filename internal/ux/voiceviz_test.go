// internal/ux/voiceviz_test.go
// Purpose: VoiceViz frame rendering and lifecycle safety — every state
// renders non-empty frames, levels clamp, and Start/Stop is idempotent and
// race-free without a TTY.
package ux

import (
	"strings"
	"testing"
)

func TestVoiceVizRendersEveryState(t *testing.T) {
	v := NewVoiceViz()
	for _, st := range []VizState{VizListening, VizTranscribing, VizSpeaking, VizStandby} {
		v.state = st
		for f := 0; f < 25; f++ {
			v.frame = f
			line := v.renderLocked()
			if strings.TrimSpace(line) == "" {
				t.Fatalf("state %d frame %d rendered empty", st, f)
			}
		}
	}
}

func TestVoiceVizLevelClamps(t *testing.T) {
	v := NewVoiceViz()
	v.SetLevel(4.2)
	if v.level != 1 {
		t.Fatalf("level must clamp to 1, got %v", v.level)
	}
	v.SetLevel(-1)
	if v.level >= 0 {
		t.Fatal("negative level must restore synthetic mode")
	}
	// A live level must not panic rendering at the extremes.
	v.SetLevel(0)
	v.state = VizListening
	_ = v.renderLocked()
	v.SetLevel(1)
	_ = v.renderLocked()
}

func TestVoiceVizLifecycleIdempotent(t *testing.T) {
	v := NewVoiceViz()
	v.tty = false // never draw in tests

	v.Start(VizListening) // non-TTY: must not start the loop
	if v.Running() {
		t.Fatal("non-TTY start must be a no-op")
	}
	v.SetState(VizSpeaking)
	v.Stop() // stop on idle must not panic
	v.Stop()
}

// The HUD redraws one terminal line in place, so anything else printing while
// it runs lands inside that line. A real session produced
//
//	● ● LISTENING |▁▂▃| 7.0sNVD update skipped/failed: context deadline exceeded
//
// LineHeld is how a background writer knows to stay quiet. It must be true only
// while a HUD is actually animating, and must return to false afterwards — a
// flag that stuck on would silence background diagnostics permanently, trading
// a cosmetic bug for a silent one.
func TestLineHeldTracksTheRunningHUD(t *testing.T) {
	if LineHeld() {
		t.Fatal("precondition: no HUD should own the line before one starts")
	}

	v := NewVoiceViz()
	v.Start(VizListening)
	held := LineHeld()
	v.Stop()

	// On a non-TTY (CI, and `go test` without a terminal) the HUD deliberately
	// does not animate, so it must not claim the line either — there is nothing
	// to corrupt and background output should flow normally.
	if v.tty && !held {
		t.Error("an animating HUD must claim the terminal line")
	}
	if !v.tty && held {
		t.Error("a HUD that does not animate must not silence background output")
	}
	if LineHeld() {
		t.Error("the line must be released when the HUD stops")
	}
}

// Stop is called from defers and error paths; releasing twice, or stopping a
// HUD that never started, must not leave the flag wrong.
func TestLineHeldSurvivesRedundantStops(t *testing.T) {
	v := NewVoiceViz()
	v.Stop()
	if LineHeld() {
		t.Fatal("stopping a HUD that never ran must not hold the line")
	}
	v.Start(VizListening)
	v.Stop()
	v.Stop()
	if LineHeld() {
		t.Error("a double Stop must leave the line released")
	}
}

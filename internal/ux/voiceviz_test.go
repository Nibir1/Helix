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

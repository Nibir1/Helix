// internal/wakeword/detection_rate_test.go
// Purpose: P7.8 — the §10 row "Wake-word detection accuracy ≥97%, measured by
// Phase 3 fixture corpus". Fixture tests existed for individual clips; no rate
// over a corpus had ever been computed.
//
// WHAT THIS CAN AND CANNOT SAY, because the §10 target is written for a
// capability the shipped default does not have. ADR-002 chose the energy detector
// as the default and was explicit that it "detects speech/loud-sound onset, not
// the phrase". So a keyword-accuracy figure is not measurable against it at all:
// it will fire on "hey helix", on "hello there", and on a dropped mug, by design.
//
// What IS measurable, and what this computes, is the detector's job as specified:
// does speech-level audio wake it, and does a quiet room leave it alone. Those two
// rates are the honest content of the ≥97% row for the `energy` engine. True
// keyword accuracy needs the openWakeWord sidecar and a live server, which is
// what the Phase 3 acceptance note already says is manual QA.
package wakeword

import (
	"testing"

	"helix/internal/speech"
)

// detectionCorpus builds the fixture set as PAIRS: the room a machine sits in,
// and an utterance spoken into that room. Pairs rather than absolute clips,
// because the detector measures the floor and judges against a multiple of it —
// so "is this loud enough to be speech" is only answerable relative to
// somewhere.
//
// The levels are the ones microphones actually produce, and that is a
// correction. This corpus used to be sine tones at amplitude 0.25-0.9 (RMS
// 0.18-0.64) for "speech" and up to 0.04 (RMS 0.028) for "room noise it must
// ignore". A MacBook Pro built-in mic at input volume 38 measures RMS 0.0011
// for a quiet room and 0.0057 for speech at 40cm (real recordings in
// testdata/, loaded by real_capture_test.go) — so the old "must not wake"
// bucket was five times LOUDER than real speech, and the old "must wake"
// bucket was thirty times louder than anything the device can emit. The suite
// was green and hands-free wake could not fire.
//
// Three device scales are covered because a fixed number cannot serve all
// three, which is the reason the detector no longer uses one.
func detectionCorpus() (cases []detectionCase) {
	const rate = 16000
	const samples = rate * 3 / 2 // 1500ms, the shipped chunk

	clip := func(rms, freq float64) speech.AudioFormat {
		return speech.AudioFormat{
			Kind: speech.KindPCM, SampleRate: rate, Channels: 1,
			Bytes: pcmToneRMS(samples, rms, freq, rate),
		}
	}

	// Each device: the room's floor, then the utterance levels to expect over
	// it. Speech runs ~10-25 dB over a quiet room on every one of them, which
	// is the invariant the ratio presets are built on.
	devices := []struct {
		name       string
		floor      float64
		utterances []float64
	}{
		// Measured: MacBook Pro built-in mic, macOS input volume 38.
		{"laptop mic, low input gain", 0.0012, []float64{0.005, 0.009, 0.02}},
		// Same class of device with input gain near the top of the slider.
		{"laptop mic, high input gain", 0.005, []float64{0.02, 0.04, 0.09}},
		// Close-talking USB condenser at unity — where the old constants lived.
		{"close USB mic at unity", 0.02, []float64{0.08, 0.15, 0.3}},
	}

	for _, dev := range devices {
		for _, freq := range []float64{180, 300, 600, 1200} {
			for _, u := range dev.utterances {
				cases = append(cases, detectionCase{
					device: dev.name,
					room:   clip(dev.floor, 60),
					probe:  clip(u, freq),
					wake:   true,
				})
			}
			// The case that actually matters in a home: a room that is not
			// silent — fridge hum, distant traffic, a keyboard — moving around
			// its own floor without a voice in it. Up to 1.5x the floor, well
			// inside the loosest preset's 2x margin.
			for _, mult := range []float64{0.5, 1.0, 1.3, 1.5} {
				cases = append(cases, detectionCase{
					device: dev.name,
					room:   clip(dev.floor, 60),
					probe:  clip(dev.floor*mult, freq),
					wake:   false,
				})
			}
		}
	}
	return cases
}

// detectionCase is one room plus one chunk spoken (or not) into it.
type detectionCase struct {
	device string
	room   speech.AudioFormat
	probe  speech.AudioFormat
	wake   bool
}

// verdict runs one case the way the scan loop would: the detector measures the
// room for a few chunks, then judges the probe.
func (c detectionCase) verdict(t *testing.T, preset Preset) (woke bool, bar, level float64) {
	t.Helper()
	d := NewEnergyDetector(preset)
	for i := 0; i < 4; i++ {
		if _, woke, err := d.Wake(c.room); err != nil {
			t.Fatalf("%s: room chunk: %v", c.device, err)
		} else if woke {
			t.Fatalf("%s: the room woke the detector while it was still "+
				"measuring the room (bar %.5f)", c.device, d.Bar())
		}
	}
	level, woke, err := d.Wake(c.probe)
	if err != nil {
		t.Fatalf("%s: probe chunk: %v", c.device, err)
	}
	return woke, d.Bar(), level
}

// TestWakeDetectionRateOnFixtureCorpus is the §10 measurement for the energy
// engine.
func TestWakeDetectionRateOnFixtureCorpus(t *testing.T) {
	const target = 97.0

	var wanted, detected, quiet, falsePositives int
	for _, c := range detectionCorpus() {
		woke, bar, level := c.verdict(t, PresetBalanced)
		switch {
		case c.wake:
			wanted++
			if woke {
				detected++
			} else {
				t.Logf("  MISSED %s: level %.5f vs bar %.5f", c.device, level, bar)
			}
		default:
			quiet++
			if woke {
				falsePositives++
				t.Logf("  FALSE  %s: level %.5f vs bar %.5f", c.device, level, bar)
			}
		}
	}

	detectRate := float64(detected) / float64(wanted) * 100
	fpRate := float64(falsePositives) / float64(quiet) * 100

	t.Logf("energy engine, balanced preset:")
	t.Logf("  detection rate:      %.1f%% (%d/%d utterances over a measured room woke it)",
		detectRate, detected, wanted)
	t.Logf("  false-positive rate: %.1f%% (%d/%d room-noise chunks woke it)",
		fpRate, falsePositives, quiet)
	t.Logf("  SCOPE: onset detection, not keyword spotting — this engine cannot")
	t.Logf("         distinguish \"hey helix\" from any other speech (ADR-002)")

	if detectRate < target {
		t.Errorf("detection rate %.1f%% is below the §10 floor of %.0f%%", detectRate, target)
	}
	if falsePositives > 0 {
		t.Errorf("%d room-noise clips triggered a wake — a false trigger costs the "+
			"user a transcription and an unwanted turn", falsePositives)
	}
}

// The preset ladder must be monotonic, or "strict" and "loose" are just names.
// This is the knob a user reaches for after measuring the rates above, so it has
// to behave the way its labels promise.
func TestPresetsAreOrderedBySensitivity(t *testing.T) {
	const rate = 16000
	const samples = rate * 3 / 2

	// A room at built-in-mic level, and a chunk 3.5x over it: between loose's
	// 2x and strict's 5x, which is where the presets are supposed to disagree.
	const floor = 0.0012
	borderline := detectionCase{
		device: "preset ladder",
		room: speech.AudioFormat{
			Kind: speech.KindPCM, SampleRate: rate, Channels: 1,
			Bytes: pcmToneRMS(samples, floor, 60, rate),
		},
		probe: speech.AudioFormat{
			Kind: speech.KindPCM, SampleRate: rate, Channels: 1,
			Bytes: pcmToneRMS(samples, floor*3.5, 300, rate),
		},
	}

	counts := map[Preset]bool{}
	for _, p := range []Preset{PresetStrict, PresetBalanced, PresetLoose} {
		woke, _, _ := borderline.verdict(t, p)
		counts[p] = woke
	}

	// Loose must never be less willing to wake than strict.
	if counts[PresetStrict] && !counts[PresetLoose] {
		t.Errorf("strict woke on a clip loose ignored — the presets are inverted: %v", counts)
	}
	t.Logf("chunk at 3.5x the measured floor: strict=%v balanced=%v loose=%v",
		counts[PresetStrict], counts[PresetBalanced], counts[PresetLoose])
}

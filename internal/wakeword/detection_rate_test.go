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

// detectionCorpus builds the fixture set: clips that should wake, and clips that
// should not.
//
// The "should wake" set spans amplitudes from a soft voice upward and several
// frequencies, since the detector is amplitude-driven and a corpus of one loud
// tone would prove nothing about a quiet speaker. The "should not" set is the
// case that actually matters in a home: a room that is not silent — fridge hum,
// distant traffic, keyboard noise — must not trigger a turn.
func detectionCorpus() (wake, quiet []speech.AudioFormat) {
	const rate = 16000
	const samples = rate * 3 / 2 // 1500ms, the shipped chunk

	clip := func(pcm []byte) speech.AudioFormat {
		return speech.AudioFormat{
			Kind: speech.KindPCM, SampleRate: rate, Channels: 1, Bytes: pcm,
		}
	}

	// Speech level: the balanced preset's threshold is normalized RMS 0.12, so a
	// sine needs amplitude ≥ ~0.17 to clear it. Real speech at conversational
	// distance sits in this band.
	for _, amp := range []float64{0.25, 0.35, 0.5, 0.7, 0.9} {
		for _, freq := range []float64{180, 300, 600, 1200} {
			wake = append(wake, clip(pcmTone(samples, amp, freq, rate)))
		}
	}

	// Room noise: audible but well below a voice addressing the machine.
	for _, amp := range []float64{0.0, 0.005, 0.01, 0.02, 0.04, 0.06} {
		for _, freq := range []float64{60, 220, 800} {
			quiet = append(quiet, clip(pcmTone(samples, amp, freq, rate)))
		}
	}
	return wake, quiet
}

// TestWakeDetectionRateOnFixtureCorpus is the §10 measurement for the energy
// engine.
func TestWakeDetectionRateOnFixtureCorpus(t *testing.T) {
	const target = 97.0

	d := NewEnergyDetector(PresetBalanced)
	wake, quiet := detectionCorpus()

	var detected int
	for _, c := range wake {
		if _, woke, err := d.Wake(c); err == nil && woke {
			detected++
		}
	}
	var falsePositives int
	for _, c := range quiet {
		if _, woke, err := d.Wake(c); err == nil && woke {
			falsePositives++
		}
	}

	detectRate := float64(detected) / float64(len(wake)) * 100
	fpRate := float64(falsePositives) / float64(len(quiet)) * 100

	t.Logf("energy engine, balanced preset:")
	t.Logf("  detection rate:      %.1f%% (%d/%d speech-level clips woke it)",
		detectRate, detected, len(wake))
	t.Logf("  false-positive rate: %.1f%% (%d/%d room-noise clips woke it)",
		fpRate, falsePositives, len(quiet))
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

	// A clip in the middle of the range, where the presets should disagree.
	borderline := speech.AudioFormat{
		Kind: speech.KindPCM, SampleRate: rate, Channels: 1,
		Bytes: pcmTone(samples, 0.2, 300, rate),
	}

	counts := map[Preset]bool{}
	for _, p := range []Preset{PresetStrict, PresetBalanced, PresetLoose} {
		_, woke, err := NewEnergyDetector(p).Wake(borderline)
		if err != nil {
			t.Fatalf("preset %s: %v", p, err)
		}
		counts[p] = woke
	}

	// Loose must never be less willing to wake than strict.
	if counts[PresetStrict] && !counts[PresetLoose] {
		t.Errorf("strict woke on a clip loose ignored — the presets are inverted: %v", counts)
	}
	t.Logf("borderline clip (rms≈0.14): strict=%v balanced=%v loose=%v",
		counts[PresetStrict], counts[PresetBalanced], counts[PresetLoose])
}

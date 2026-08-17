// internal/ambient/analyzer_test.go
// Purpose: Phase 6 (P6.4) — golden classification of synthetic fixtures with
// no audio hardware: silence, a pure tone (alarm-like), broadband noise
// (loud_noise), and a many-tone chord (music-like).
package ambient

import (
	"math"
	"math/rand"
	"testing"
)

const testWindow = 1024

// sine returns a bin-aligned sine at FFT bin k (zero spectral leakage with a
// rectangular window), so classification is deterministic.
func sine(k int, amp float64) []float64 {
	out := make([]float64, testWindow)
	for i := range out {
		out[i] = amp * math.Sin(2*math.Pi*float64(k)*float64(i)/float64(testWindow))
	}
	return out
}

func whiteNoise(rng *rand.Rand, amp float64) []float64 {
	out := make([]float64, testWindow)
	for i := range out {
		out[i] = amp * (2*rng.Float64() - 1)
	}
	return out
}

func chord(bins []int, amp float64) []float64 {
	out := make([]float64, testWindow)
	for _, k := range bins {
		for i := range out {
			out[i] += amp * math.Sin(2*math.Pi*float64(k)*float64(i)/float64(testWindow))
		}
	}
	return out
}

func TestAnalyzerSilence(t *testing.T) {
	a := Analyzer{Sensitivity: 0.5}
	dets := a.Analyze(make([]float64, testWindow))
	if len(dets) != 1 || dets[0].Category != CategorySilence {
		t.Fatalf("silence must classify as silence, got %+v", dets)
	}
}

func TestAnalyzerAlarmLike(t *testing.T) {
	a := Analyzer{Sensitivity: 0.5}
	dets := a.Analyze(sine(50, 0.8)) // single narrow band
	if len(dets) != 1 || dets[0].Category != CategoryAlarmLike {
		t.Fatalf("pure tone must classify as alarm_like, got %+v", dets)
	}
}

func TestAnalyzerLoudNoise(t *testing.T) {
	a := Analyzer{Sensitivity: 0.5}
	rng := rand.New(rand.NewSource(1))
	dets := a.Analyze(whiteNoise(rng, 1.0)) // flat broadband spectrum
	if len(dets) != 1 || dets[0].Category != CategoryLoudNoise {
		t.Fatalf("white noise must classify as loud_noise, got %+v", dets)
	}
}

func TestAnalyzerMusicLike(t *testing.T) {
	a := Analyzer{Sensitivity: 0.5}
	bins := []int{20, 40, 60, 80, 100, 120, 140, 160}
	// 8 tones at 0.15 each → RMS ≈ 0.30, above the 0.20 loudness threshold.
	dets := a.Analyze(chord(bins, 0.15))
	if len(dets) != 1 || dets[0].Category != CategoryMusicLike {
		t.Fatalf("multi-tone chord must classify as music_like, got %+v", dets)
	}
}

func TestAnalyzerQuietNotSilent(t *testing.T) {
	// Just above the silence floor but below the loudness threshold: no event.
	a := Analyzer{Sensitivity: 0.5}
	dets := a.Analyze(sine(10, 0.02))
	if len(dets) != 0 {
		t.Fatalf("quiet-but-audible must not fire any event, got %+v", dets)
	}
}

func TestAnalyzerSensitivityClamped(t *testing.T) {
	// Sensitivity out of range must be clamped, not panic.
	a := Analyzer{Sensitivity: 99}
	if got := a.Analyze(whiteNoise(rand.New(rand.NewSource(2)), 0.1)); len(got) != 0 {
		t.Fatalf("sensitivity 99 clamps to 1 (most sensitive); expected a loud event on noise, got %+v", got)
	}
}

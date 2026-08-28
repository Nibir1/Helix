// internal/ambient/fuzz_test.go
// Purpose: Phase 7 (P7.5) — fuzz the ambient analyzer. Invariant: never panic
// and only emit known categories with in-range intensity. Fuzz input is []byte
// (Go fuzzing's slice type) mapped onto a normalized PCM window.
package ambient

import "testing"

func FuzzAnalyzer(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0})
	f.Add([]byte{255, 128, 0, 128})

	f.Fuzz(func(t *testing.T, data []byte) {
		samples := make([]float64, len(data))
		for i, b := range data {
			samples[i] = (float64(b) - 128) / 128
		}
		a := Analyzer{Sensitivity: 0.5}
		for _, d := range a.Analyze(samples) {
			switch d.Category {
			case CategorySilence, CategoryLoudNoise, CategoryAlarmLike, CategoryMusicLike:
			default:
				t.Fatalf("unknown category %q", d.Category)
			}
			if d.Intensity < 0 || d.Intensity > 1 {
				t.Fatalf("intensity out of range: %v", d.Intensity)
			}
		}
	})
}

// internal/ambient/analyzer.go
// Purpose: BlackBox Phase 6 (P6.1) — pure-Go, rule-based ambient audio
// analysis. One window of PCM samples is reduced to RMS energy and a crude
// spectral concentration (how much energy sits in the loudest few FFT bins),
// then classified into silence / loud_noise / alarm_like / music_like.
//
// Honesty contract: these are heuristics, not a trained classifier. A pure
// tone reads as alarm_like (narrow band), broadband noise reads as loud_noise,
// and a many-tone mixture reads as music_like. YAMNet-class classifiers are
// explicitly deferred (roadmap P6.1).
package ambient

import "math"

// Analyzer classifies one window of normalized PCM samples (typically [-1,1]).
type Analyzer struct {
	// Sensitivity scales the loudness threshold: higher = more sensitive.
	// Defaults to 0.5; clamped to [0,1].
	Sensitivity float64
}

// Detection is one classified event for a window.
type Detection struct {
	Category  Category
	Intensity float64 // 0..1, relative to the loudness threshold
}

// Analyze classifies a window of PCM samples. It returns at most one event per
// window (the dominant category); an empty result means "quiet but not silent".
func (a Analyzer) Analyze(samples []float64) []Detection {
	if len(samples) < 16 {
		return nil
	}

	sens := a.Sensitivity
	if sens < 0 {
		sens = 0
	}
	if sens > 1 {
		sens = 1
	}

	rms := computeRMS(samples)

	// Silence: near-zero energy for the whole window.
	const silenceFloor = 0.01
	if rms < silenceFloor {
		return []Detection{{Category: CategorySilence, Intensity: 1 - rms/silenceFloor}}
	}

	// Loudness threshold: 0.30 at sensitivity 0 down to 0.10 at sensitivity 1.
	loud := rms >= 0.30-0.20*sens

	concentration := spectralConcentration(samples)

	switch {
	case loud && concentration >= 0.5:
		// Sustained narrow band (e.g. a beeping alarm).
		return []Detection{{Category: CategoryAlarmLike, Intensity: intensityOf(rms, loudThreshold(sens))}}
	case loud && concentration >= 0.15:
		// Multiple distinct tones → chord/music-like.
		return []Detection{{Category: CategoryMusicLike, Intensity: intensityOf(rms, loudThreshold(sens))}}
	case loud:
		// Broadband, flat-spectrum energy → a loud noise burst.
		return []Detection{{Category: CategoryLoudNoise, Intensity: intensityOf(rms, loudThreshold(sens))}}
	default:
		// Quiet-but-audible: no event (no spam).
		return nil
	}
}

func loudThreshold(sens float64) float64 { return 0.30 - 0.20*sens }

// intensityOf scales loudness above the threshold into a 0..1 value.
func intensityOf(rms, threshold float64) float64 {
	if threshold <= 0 {
		return 1
	}
	v := (rms - threshold) / (1 - threshold)
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return v
}

// computeRMS returns the root-mean-square of the window.
func computeRMS(samples []float64) float64 {
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// spectralConcentration returns the fraction of total spectral energy held by
// the three loudest bins: ~1 for a pure tone, ~0 for flat broadband noise,
// and intermediate for a few-tone mixture.
func spectralConcentration(samples []float64) float64 {
	mag := magnitudeSpectrum(samples)
	if len(mag) < 4 {
		return 0
	}

	var total float64
	top := [3]float64{}
	for _, m := range mag {
		total += m
		for i := range top {
			if m > top[i] {
				copy(top[i+1:], top[i:])
				top[i] = m
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return (top[0] + top[1] + top[2]) / total
}

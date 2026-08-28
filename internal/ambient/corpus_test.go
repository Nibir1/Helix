// internal/ambient/corpus_test.go
// Purpose: BlackBox Phase 6 acceptance — "≥90% accuracy on the configured
// fixture categories", measured rather than asserted one fixture at a time.
//
// The golden tests next door prove one representative per category, which is
// enough to catch a broken classifier and not enough to support an accuracy
// CLAIM. This builds a corpus that spans the analyzer's real operating range —
// several amplitudes, several frequencies, several noise seeds, chords of
// varying density — scores it, and reports the number.
//
// Honest about what it does NOT measure (P6.1's stated contract): these are
// synthetic signals, so this is the accuracy of the RULES against the acoustic
// shapes they were written for, not accuracy against real-world audio. A real
// alarm, a real room, and a real song need a trained classifier, which the
// roadmap defers to a future sidecar. Saying so here keeps the 90% figure from
// being read as something it is not.
package ambient

import (
	"fmt"
	"math/rand"
	"testing"
)

// fixture is one labelled synthetic window.
type fixture struct {
	name string
	want Category
	data []float64
}

// buildCorpus generates the labelled fixture set.
//
// The amplitudes are chosen from the analyzer's own thresholds (silence floor
// 0.01; loudness 0.30-0.20·sensitivity, so 0.20 at the default 0.5) so each
// group sits where that category genuinely lives, including a few cases close
// to a boundary — an accuracy target with no near-misses in it measures
// nothing.
func buildCorpus() []fixture {
	var out []fixture

	// ---- silence: below the 0.01 RMS floor ----
	out = append(out, fixture{"silence/pure-zero", CategorySilence, make([]float64, testWindow)})
	for i, amp := range []float64{0.0005, 0.001, 0.002, 0.004, 0.008} {
		rng := rand.New(rand.NewSource(int64(100 + i)))
		out = append(out, fixture{
			name: fmt.Sprintf("silence/dither-%.4f", amp),
			want: CategorySilence,
			data: whiteNoise(rng, amp),
		})
	}
	for i, bin := range []int{5, 40} {
		out = append(out, fixture{
			name: fmt.Sprintf("silence/faint-tone-bin%d", bin),
			want: CategorySilence,
			data: sine(bin, 0.004+0.002*float64(i)),
		})
	}

	// ---- alarm_like: loud AND narrow band (concentration ≥ 0.5) ----
	for _, bin := range []int{20, 50, 80, 110, 140, 200, 260, 330} {
		for _, amp := range []float64{0.4, 0.7, 1.0} {
			out = append(out, fixture{
				name: fmt.Sprintf("alarm/tone-bin%d-amp%.1f", bin, amp),
				want: CategoryAlarmLike,
				data: sine(bin, amp),
			})
		}
	}
	// Two-tone sirens are still narrow band: the top three bins hold nearly
	// everything, so these must not drift into music_like.
	for _, pair := range [][]int{{60, 62}, {90, 180}, {120, 240}} {
		out = append(out, fixture{
			name: fmt.Sprintf("alarm/two-tone-%d+%d", pair[0], pair[1]),
			want: CategoryAlarmLike,
			data: chord(pair, 0.5),
		})
	}

	// ---- music_like: loud with several distinct tones (0.15 ≤ conc < 0.5) ----
	chords := [][]int{
		{20, 40, 60, 80, 100, 120, 140, 160},
		{30, 45, 60, 75, 90, 105, 135},
		{25, 50, 75, 100, 125, 150, 175, 200, 225, 250},
		{16, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192},
		{35, 70, 105, 140, 175, 210, 245},
	}
	for i, bins := range chords {
		// Per-tone amplitude chosen so total RMS = amp·sqrt(n/2) clears the
		// 0.20 loudness threshold with margin.
		for _, amp := range []float64{0.15, 0.25} {
			out = append(out, fixture{
				name: fmt.Sprintf("music/chord%d-%dtones-amp%.2f", i, len(bins), amp),
				want: CategoryMusicLike,
				data: chord(bins, amp),
			})
		}
	}

	// ---- loud_noise: loud and broadband (flat spectrum) ----
	for i, amp := range []float64{0.6, 0.8, 1.0, 1.2} {
		for seed := 0; seed < 3; seed++ {
			rng := rand.New(rand.NewSource(int64(seed*31 + i)))
			out = append(out, fixture{
				name: fmt.Sprintf("noise/amp%.1f-seed%d", amp, seed),
				want: CategoryLoudNoise,
				data: whiteNoise(rng, amp),
			})
		}
	}

	return out
}

// TestAnalyzerCorpusAccuracy is the Phase 6 acceptance measurement.
func TestAnalyzerCorpusAccuracy(t *testing.T) {
	const target = 0.90

	a := Analyzer{Sensitivity: 0.5}
	corpus := buildCorpus()
	if len(corpus) < 40 {
		t.Fatalf("corpus too small to support an accuracy claim: %d fixtures", len(corpus))
	}

	type score struct{ hit, total int }
	per := map[Category]*score{}
	var hits int
	var misses []string

	for _, f := range corpus {
		if per[f.want] == nil {
			per[f.want] = &score{}
		}
		per[f.want].total++

		dets := a.Analyze(f.data)
		var got Category
		if len(dets) == 1 {
			got = dets[0].Category
		}
		if got == f.want {
			hits++
			per[f.want].hit++
			continue
		}
		misses = append(misses, fmt.Sprintf("%s: got %q, want %q", f.name, got, f.want))
	}

	accuracy := float64(hits) / float64(len(corpus))
	t.Logf("ambient corpus accuracy: %.1f%% (%d/%d fixtures)",
		accuracy*100, hits, len(corpus))
	for cat, s := range per {
		t.Logf("  %-12s %.1f%% (%d/%d)", cat,
			float64(s.hit)/float64(s.total)*100, s.hit, s.total)
	}
	for _, m := range misses {
		t.Logf("  miss: %s", m)
	}

	if accuracy < target {
		t.Fatalf("corpus accuracy %.1f%% is below the %.0f%% acceptance target",
			accuracy*100, target*100)
	}

	// Per-category too: an aggregate can hide one category failing completely
	// when the others are over-represented, and a category that never fires is
	// exactly the failure a user would notice.
	for cat, s := range per {
		catAcc := float64(s.hit) / float64(s.total)
		if catAcc < target {
			t.Errorf("category %q accuracy %.1f%% (%d/%d) is below the %.0f%% target",
				cat, catAcc*100, s.hit, s.total, target*100)
		}
	}
}

// Quiet-but-audible must stay silent across the whole band between the silence
// floor and the loudness threshold. This is the no-spam guarantee at the
// analyzer level: an assistant that asks "are you okay?" because a chair
// creaked is worse than one that says nothing.
func TestAnalyzerQuietBandNeverFires(t *testing.T) {
	a := Analyzer{Sensitivity: 0.5}
	for _, amp := range []float64{0.015, 0.03, 0.06, 0.1, 0.15, 0.2} {
		// RMS of a sine is amp/√2, so these span the floor up to just under
		// the 0.20 loudness threshold.
		if dets := a.Analyze(sine(64, amp)); len(dets) != 0 {
			t.Errorf("tone amp %.3f (rms %.3f) must not fire an event, got %+v",
				amp, computeRMS(sine(64, amp)), dets)
		}
	}
}

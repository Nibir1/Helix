// internal/speech/level_test.go
// Purpose: BlackBox P12.4 — the HUD level meter maps real microphone RMS onto
// a range that actually moves for ordinary speech.
package speech

import (
	"math"
	"testing"
)

func TestLevelFromRMSSpansSpeechRange(t *testing.T) {
	cases := []struct {
		name string
		rms  float64
		min  float64
		max  float64
	}{
		// Digital silence and sub-floor noise must read empty, or the HUD
		// would look "live" with a dead mic — the exact lie it must not tell.
		{"digital silence", 0, 0, 0},
		{"below floor (-60 dBFS)", 0.001, 0, 0},
		// A quiet room through to close talking must span most of the meter.
		{"quiet speech (-40 dBFS)", 0.01, 0.2, 0.35},
		{"normal speech (-30 dBFS)", 0.0316, 0.45, 0.6},
		{"loud speech (-20 dBFS)", 0.1, 0.7, 0.85},
		{"clipping (-6 dBFS)", 0.5, 1, 1},
	}
	for _, tc := range cases {
		got := levelFromRMS(tc.rms)
		if got < tc.min || got > tc.max {
			t.Errorf("%s: level(%.4f) = %.3f, want within [%.2f, %.2f]",
				tc.name, tc.rms, got, tc.min, tc.max)
		}
	}
}

func TestLevelFromRMSIsMonotonic(t *testing.T) {
	prev := -1.0
	for rms := 0.001; rms < 1.0; rms *= 1.2 {
		got := levelFromRMS(rms)
		if got < prev {
			t.Fatalf("meter must be monotonic: level(%.4f) = %.3f < previous %.3f",
				rms, got, prev)
		}
		if got < 0 || got > 1 {
			t.Fatalf("level(%.4f) = %.3f is outside 0..1", rms, got)
		}
		prev = got
	}
}

// A linear meter is the thing this replaces: driven by RMS it barely leaves the
// floor for ordinary speech, which is why the waveform looked dead. The log
// mapping must put normal speech near the middle, not near zero.
func TestLevelMappingBeatsLinearForSpeech(t *testing.T) {
	const normalSpeechRMS = 0.0316 // -30 dBFS

	logLevel := levelFromRMS(normalSpeechRMS)
	linear := normalSpeechRMS // what a naive meter would show

	if logLevel <= linear*4 {
		t.Fatalf("log meter (%.3f) should lift normal speech well clear of the "+
			"linear reading (%.3f)", logLevel, linear)
	}
	if logLevel < 0.35 || logLevel > 0.7 {
		t.Fatalf("normal speech should sit mid-meter, got %.3f", logLevel)
	}
}

func TestClipLevelHandlesUndecodableAudio(t *testing.T) {
	// Malformed audio must read as silence, never panic: this runs on every
	// captured chunk in the live loop.
	if got := ClipLevel(AudioFormat{Bytes: []byte("not a wav")}); got != 0 {
		t.Fatalf("undecodable audio must read 0, got %.3f", got)
	}
	if got := ClipLevel(AudioFormat{}); got != 0 {
		t.Fatalf("empty audio must read 0, got %.3f", got)
	}
}

func TestClipLevelTracksRealSignal(t *testing.T) {
	quiet := ClipLevel(syntheticWAV(t, 0.005))
	loud := ClipLevel(syntheticWAV(t, 0.3))

	if !(loud > quiet) {
		t.Fatalf("a louder clip must read higher: quiet=%.3f loud=%.3f", quiet, loud)
	}
	if loud < 0.7 {
		t.Fatalf("a loud clip should read near full scale, got %.3f", loud)
	}
}

// syntheticWAV builds a 16-bit mono WAV of a sine at the given amplitude.
func syntheticWAV(t *testing.T, amplitude float64) AudioFormat {
	t.Helper()
	const rate = 16000
	const frames = rate / 4 // 250ms

	samples := make([]int16, frames)
	for i := range samples {
		samples[i] = int16(amplitude * 32767 * math.Sin(2*math.Pi*440*float64(i)/rate))
	}
	return AudioFormat{
		Kind: "wav", SampleRate: rate, Channels: 1,
		Bytes: makeWAV(samples, rate, 1),
	}
}

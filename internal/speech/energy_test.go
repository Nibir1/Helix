// internal/speech/energy_test.go
// Purpose: RMS/speech-gate helpers (energy.go) — the voice loop's amplitude
// gate must reject dead-mic silence while passing quiet-but-real speech.
package speech

import (
	"math"
	"testing"
)

// sineClip builds a WAV at the given amplitude (0..1, full-scale = 1).
func sineClip(amp float64, n int) AudioFormat {
	samples := make([]int16, n)
	for i := range samples {
		samples[i] = int16(amp * 32767 * math.Sin(2*math.Pi*440*float64(i)/16000))
	}
	return AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: makeWAV(samples, 16000, 1)}
}

func silenceClip(n int) AudioFormat {
	return AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: makeWAV(make([]int16, n), 16000, 1)}
}

func TestClipRMS(t *testing.T) {
	// Digital silence → RMS 0.
	if rms := ClipRMS(silenceClip(1600)); rms != 0 {
		t.Fatalf("silence RMS = %v, want 0", rms)
	}
	// A full-scale sine has RMS ≈ 0.707; amplitude 0.1 → ≈ 0.0707.
	if rms := ClipRMS(sineClip(1.0, 8000)); math.Abs(rms-0.707) > 0.02 {
		t.Fatalf("full-scale RMS = %v, want ≈0.707", rms)
	}
	if rms := ClipRMS(sineClip(0.1, 8000)); math.Abs(rms-0.0707) > 0.01 {
		t.Fatalf("0.1-amplitude RMS = %v, want ≈0.0707", rms)
	}
	// Undecodable/empty buffers → 0, never a panic.
	if rms := ClipRMS(AudioFormat{}); rms != 0 {
		t.Fatalf("empty RMS = %v, want 0", rms)
	}
}

func TestHasSpeech(t *testing.T) {
	// Silence fails the gate even with the default floor.
	if HasSpeech(silenceClip(1600), 0) {
		t.Fatal("silence must not count as speech")
	}
	// Quiet-but-real speech (≈ -20 dBFS) passes.
	if !HasSpeech(sineClip(0.1, 8000), 0) {
		t.Fatal("0.1-amplitude tone must count as speech")
	}
	// A custom floor is honored.
	if HasSpeech(sineClip(0.01, 8000), 0.05) {
		t.Fatal("tone below a 0.05 floor must be rejected")
	}
}

// TestUsableSpeech pins the QA regression directly: the recorder used to hand
// back a ~0.1s clip of Helix's own 880Hz ready chime, which cleared the
// amplitude gate and came back from STT as the word "you". Loud is not enough —
// it also has to be long enough to be a command.
func TestUsableSpeech(t *testing.T) {
	cases := []struct {
		name string
		clip AudioFormat
		want bool
	}{
		// 0.1s at 16 kHz = 1600 samples, at chime amplitude: the exact shape of
		// the clip that produced the phantom turn.
		{"loud 0.1s chime tail", sineClip(0.8, 1600), false},
		// 0.5s of ordinary speech level: the shortest genuine utterance.
		{"0.5s speech", sineClip(0.1, 8000), true},
		// Long but silent: still nothing to transcribe.
		{"1s silence", silenceClip(16000), false},
		// Right at the boundary (0.3s = 4800 samples) — accepted.
		{"0.3s speech", sineClip(0.1, 4800), true},
		{"undecodable", AudioFormat{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UsableSpeech(tc.clip); got != tc.want {
				t.Fatalf("UsableSpeech = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClipDuration(t *testing.T) {
	// 1 second at 16 kHz.
	if d := ClipDuration(sineClip(0.5, 16000)); math.Abs(d-1.0) > 0.01 {
		t.Fatalf("duration = %v, want ≈1.0s", d)
	}
	if d := ClipDuration(AudioFormat{}); d != 0 {
		t.Fatalf("empty duration = %v, want 0", d)
	}
}

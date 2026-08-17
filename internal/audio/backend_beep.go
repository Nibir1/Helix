//go:build !linux || audio_cgo

// internal/audio/backend_beep.go
//
// Purpose: beep/oto synthesis backend with the approved Helix voice set
// (350Hz data tap, 880Hz alert ping, 110Hz error buzz).
//
// Build tags:
//   - Always used on macOS/Windows (oto is CGO-free there via purego).
//   - On Linux only used with `-tags audio_cgo` (requires libasound2-dev),
//     because oto's ALSA layer is CGO-based on Linux.
package audio

import (
	"math"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// backendInit initializes the speaker with the proven 50ms buffer.
//
// Args: none.
// Returns: error if speaker initialization fails.
// Complexity: O(1), plus OS audio-device initialization time.
func backendInit() error {
	sr := beep.SampleRate(SampleRate)
	return speaker.Init(sr, sr.N(time.Second/20))
}

// backendPlayClick renders the original 350Hz percussive data tap.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func backendPlayClick() {
	// 350Hz = solid "Terminal" sound.
	sine := &SineWave{Freq: 350, Phase: 0}

	// Very short duration: 25ms.
	duration := beep.Take(SampleRate/40, sine)
	start := time.Now()

	click := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = duration.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*40))

			samples[i][0] *= (0.15 * vol)
			samples[i][1] *= (0.15 * vol)
		}
		return n, ok
	})

	speaker.Play(click)
}

// backendPlayTick renders the typewriter tick using the same strong voice.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func backendPlayTick() {
	sine := &SineWave{Freq: 350, Phase: 0}
	duration := beep.Take(SampleRate/40, sine)
	start := time.Now()

	tick := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = duration.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*40))

			samples[i][0] *= (0.15 * vol)
			samples[i][1] *= (0.15 * vol)
		}
		return n, ok
	})

	speaker.Play(tick)
}

// backendPlayAlert renders the strong 880Hz high-tech ping.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func backendPlayAlert() {
	sine := &SineWave{Freq: 880, Phase: 0}
	beepSound := beep.Take(SampleRate/5, sine)
	start := time.Now()

	alert := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = beepSound.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*3))

			samples[i][0] = samples[i][0] * 0.8 * vol
			samples[i][1] = samples[i][1] * 0.8 * vol
		}
		return n, ok
	})

	speaker.Play(alert)
}

// backendPlayError renders the 110Hz sawtooth buzz.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func backendPlayError() {
	saw := &SawWave{Freq: 110}
	buzz := beep.Take(SampleRate/5, saw)
	start := time.Now()

	errorSound := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = buzz.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*4))

			samples[i][0] *= (0.2 * vol)
			samples[i][1] *= (0.2 * vol)
		}
		return n, ok
	})

	speaker.Play(errorSound)
}

// backendPlaySpeech plays a decoded speech streamer to completion.
//
// Args:
//   - s: fully decoded (and resampled) speech streamer.
//
// Returns: error if playback cannot be scheduled.
// Complexity: O(duration).
func backendPlaySpeech(s beep.Streamer) error {
	done := make(chan struct{})
	speaker.Play(beep.Seq(s, beep.Callback(func() { close(done) })))
	<-done
	return nil
}

// SineWave generates a simple sine tone.
type SineWave struct {
	Freq  float64
	Phase float64
}

// Stream implements beep.Streamer.
//
// Args:
//   - samples: output sample buffer.
//
// Returns:
//   - int: number of samples written.
//   - bool: true while the stream is active.
//
// Complexity: O(len(samples)).
func (s *SineWave) Stream(samples [][2]float64) (int, bool) {
	step := s.Freq * 2 * math.Pi / float64(SampleRate)

	for i := range samples {
		v := math.Sin(s.Phase)

		samples[i][0] = v
		samples[i][1] = v

		s.Phase += step
	}

	return len(samples), true
}

// Err implements beep.Streamer.
//
// Args: none.
// Returns: nil.
// Complexity: O(1).
func (s *SineWave) Err() error { return nil }

// SawWave generates a simple sawtooth tone.
type SawWave struct {
	Freq  float64
	Phase float64
}

// Stream implements beep.Streamer.
//
// Args:
//   - samples: output sample buffer.
//
// Returns:
//   - int: number of samples written.
//   - bool: true while the stream is active.
//
// Complexity: O(len(samples)).
func (s *SawWave) Stream(samples [][2]float64) (int, bool) {
	step := s.Freq / float64(SampleRate)

	for i := range samples {
		v := (s.Phase * 2.0) - 1.0

		samples[i][0] = v
		samples[i][1] = v

		s.Phase += step
		if s.Phase > 1.0 {
			s.Phase -= 1.0
		}
	}

	return len(samples), true
}

// Err implements beep.Streamer.
//
// Args: none.
// Returns: nil.
// Complexity: O(1).
func (s *SawWave) Err() error { return nil }

// backendName identifies this build's audio backend for /doctor (P10.3).
func backendName() string { return "beep/oto (speaker output available)" }

// backendSpeechSupported reports whether TTS audio can actually be heard.
func backendSpeechSupported() bool { return true }

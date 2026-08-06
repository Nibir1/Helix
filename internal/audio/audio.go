// internal/audio/audio.go
//
// Purpose: Tonal feedback engine for Helix. Restores the proven strong voice
// set (350Hz data tap, 880Hz alert ping, 110Hz error buzz) on the proven
// 50ms speaker buffer, plus a typewriter tick that reuses the strong tap
// voice so typing feels powerful and on-time.
package audio

import (
	"math"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/speaker"
)

// SampleRate defines CD-quality audio.
const SampleRate = 44100

var (
	// mu protects all mutable audio engine state.
	mu sync.Mutex

	// initialized is true only after speaker.Init has succeeded.
	initialized bool

	// enabled is the user-facing /audio on|off toggle.
	enabled = true

	// lastType throttles typewriter ticks.
	lastType time.Time
)

// SetEnabled toggles the user-facing audio switch.
//
// Args:
//   - on: true to enable audio, false to disable.
//
// Returns: none.
// Complexity: O(1).
func SetEnabled(on bool) {
	mu.Lock()
	enabled = on
	mu.Unlock()
}

// IsEnabled reports whether the user has enabled audio.
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func IsEnabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// IsReady reports whether the speaker has been successfully initialized.
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func IsReady() bool {
	mu.Lock()
	defer mu.Unlock()
	return initialized
}

// Init initializes the speaker during normal startup.
//
// Args: none.
// Returns: error if speaker initialization fails.
// Complexity: O(1), plus OS audio-device initialization time.
func Init() error {
	return initSpeaker()
}

// EnsureReady initializes or retries speaker initialization.
//
// Args:
//   - force: retained for API compatibility with /audio on.
//
// Returns: error if speaker initialization fails.
// Complexity: O(1), plus OS audio-device initialization time.
func EnsureReady(force bool) error {
	_ = force // explicit user action always retries via initSpeaker
	return initSpeaker()
}

// initSpeaker performs guarded speaker initialization.
//
// Args: none.
// Returns: error if initialization fails.
// Complexity: O(1), plus OS audio-device initialization time.
func initSpeaker() error {
	mu.Lock()
	defer mu.Unlock()

	if initialized {
		return nil
	}

	sr := beep.SampleRate(SampleRate)

	// PROVEN VALUE: 50ms buffer (time.Second/20).
	// The 20ms experiment regressed device initialization on real hardware
	// and silenced the whole engine. 50ms is the value the original Helix
	// audio shipped with and that CoreAudio accepts reliably. A constant
	// 50ms offset is below the perception threshold for rhythm sync, so
	// ticks still feel locked to the typewriter.
	err := speaker.Init(sr, sr.N(time.Second/20))
	if err != nil {
		return err
	}

	initialized = true
	return nil
}

// playbackAllowed gates normal sound effects.
//
// Args: none.
// Returns: bool indicating whether a sound may be played.
// Complexity: O(1), plus possible lazy initialization.
func playbackAllowed() bool {
	if !IsEnabled() {
		return false
	}

	if err := initSpeaker(); err != nil {
		return false
	}

	return IsReady()
}

// PlayClick generates the original clean "Sci-Fi Data Tap" sound.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func PlayClick() {
	if !playbackAllowed() {
		return
	}

	// 350Hz = solid "Terminal" sound.
	sine := &SineWave{Freq: 350, Phase: 0}

	// Very short duration: 25ms.
	duration := beep.Take(SampleRate/40, sine)
	start := time.Now()

	// Envelope: sharp attack, fast percussive decay.
	click := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = duration.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*40))

			// Original strong volume: 15%.
			samples[i][0] *= (0.15 * vol)
			samples[i][1] *= (0.15 * vol)
		}
		return n, ok
	})

	speaker.Play(click)
	time.Sleep(50 * time.Millisecond)
}

// PlayType plays the typewriter tick using the SAME strong voice as
// PlayClick so typing feels powerful and immediate.
//
// This function is non-blocking and never retries speaker initialization,
// so typing can never stall on audio.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func PlayType() {
	if !IsEnabled() {
		return
	}

	if !IsReady() {
		return
	}

	mu.Lock()
	// Tight 10ms throttle: keeps the rhythm locked to the typewriter
	// without stacking hundreds of streams.
	if time.Since(lastType) < 10*time.Millisecond {
		mu.Unlock()
		return
	}
	lastType = time.Now()
	mu.Unlock()

	// Identical voice to the original PlayClick: strong, percussive.
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

// PlayAlert generates the original strong high-tech sine ping.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio playback time.
func PlayAlert() {
	if !playbackAllowed() {
		return
	}

	// 880Hz Sine Wave (High A) - sharp attention grabber.
	sine := &SineWave{Freq: 880, Phase: 0}

	// Full 200ms body.
	beepSound := beep.Take(SampleRate/5, sine)
	start := time.Now()

	alert := beep.StreamerFunc(func(samples [][2]float64) (n int, ok bool) {
		n, ok = beepSound.Stream(samples)
		for i := range samples[:n] {
			elapsed := time.Since(start).Seconds()
			vol := math.Max(0, 1.0-(elapsed*3))

			// Original strong volume: 80% with slow decay.
			samples[i][0] = samples[i][0] * 0.8 * vol
			samples[i][1] = samples[i][1] * 0.8 * vol
		}
		return n, ok
	})

	speaker.Play(alert)
	time.Sleep(50 * time.Millisecond)
}

// PlayError generates the original low buzz (Sawtooth-ish).
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio playback time.
func PlayError() {
	if !playbackAllowed() {
		return
	}

	// 110Hz low buzz.
	saw := &SawWave{Freq: 110}

	// 200ms duration.
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
	time.Sleep(50 * time.Millisecond)
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

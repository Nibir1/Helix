// internal/audio/audio.go
//
// Purpose: Tonal feedback engine API for Helix. Owns enable/ready state and
// typewriter throttling, and delegates actual synthesis to a platform backend:
//   - backend_beep.go : real synthesis (non-Linux, or Linux with -tags audio_cgo)
//   - backend_noop.go : silent fallback (Linux without ALSA/cgo) so every
//     build — CI, GoReleaser, contributor machines — compiles first try.
package audio

import (
	"sync"
	"time"
)

// SampleRate defines CD-quality audio.
const SampleRate = 44100

var (
	// mu protects all mutable audio engine state.
	mu sync.Mutex

	// initialized is true only after backendInit has succeeded.
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

// IsReady reports whether the backend has been successfully initialized.
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func IsReady() bool {
	mu.Lock()
	defer mu.Unlock()
	return initialized
}

// Init initializes the platform audio backend once at startup.
//
// Args: none.
// Returns: error if backend initialization fails.
// Complexity: O(1), plus OS audio-device initialization time.
func Init() error {
	mu.Lock()
	defer mu.Unlock()

	if initialized {
		return nil
	}

	if err := backendInit(); err != nil {
		return err
	}

	initialized = true
	return nil
}

// EnsureReady retries backend initialization (used by /audio on).
//
// Args:
//   - force: retained for API compatibility.
//
// Returns: error if backend initialization fails.
// Complexity: O(1), plus OS audio-device initialization time.
func EnsureReady(force bool) error {
	_ = force // explicit user action always retries via Init
	return Init()
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

	if err := Init(); err != nil {
		return false
	}

	return IsReady()
}

// PlayClick generates the clean "Sci-Fi Data Tap" sound.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio scheduling time.
func PlayClick() {
	if !playbackAllowed() {
		return
	}

	backendPlayClick()

	// Let the percussive tap land before the prompt continues.
	time.Sleep(50 * time.Millisecond)
}

// PlayType plays the typewriter tick synchronized with text rendering.
//
// Non-blocking and never retries backend initialization, so typing can
// never stall on audio.
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

	backendPlayTick()
}

// PlayAlert generates the strong high-tech sine ping.
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio playback time.
func PlayAlert() {
	if !playbackAllowed() {
		return
	}

	backendPlayAlert()
	time.Sleep(50 * time.Millisecond)
}

// PlayError generates the low buzz (Sawtooth-ish).
//
// Args: none.
// Returns: none.
// Complexity: O(1), plus audio playback time.
func PlayError() {
	if !playbackAllowed() {
		return
	}

	backendPlayError()
	time.Sleep(50 * time.Millisecond)
}

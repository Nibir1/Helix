// internal/audio/audio_test.go
//
// Purpose: Verify audio toggle behavior without requiring a real sound device.
package audio

import "testing"

// TestSetEnabled verifies that the audio toggle can be changed safely.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestSetEnabled(t *testing.T) {
	SetEnabled(false)
	if IsEnabled() {
		t.Fatal("expected audio to be disabled")
	}

	SetEnabled(true)
	if !IsEnabled() {
		t.Fatal("expected audio to be enabled")
	}
}

// TestPlayTypeWhenDisabledDoesNotPanic ensures typewriter audio is safe
// when audio is disabled.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestPlayTypeWhenDisabledDoesNotPanic(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)

	PlayType()
}

// TestPlayAlertWhenDisabledDoesNotPanic ensures alert audio is safe when
// audio is disabled.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestPlayAlertWhenDisabledDoesNotPanic(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)

	PlayAlert()
}

// internal/utils/debug_test.go
// Purpose: Verify the debug-mode priority model: runtime /debug toggle wins
// over the HELIX_DEBUG environment variable; env var governs when not toggled.
package utils

import "testing"

// TestIsDebugModePriority exercises toggle > env precedence in both directions.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestIsDebugModePriority(t *testing.T) {
	resetDebugMode()
	defer resetDebugMode()

	t.Setenv("HELIX_DEBUG", "")
	if IsDebugMode() {
		t.Fatal("expected OFF with no env and no toggle")
	}

	t.Setenv("HELIX_DEBUG", "1")
	if !IsDebugMode() {
		t.Fatal("expected HELIX_DEBUG=1 to enable when not toggled")
	}

	SetDebugMode(false)
	if IsDebugMode() {
		t.Fatal("expected /debug off to override HELIX_DEBUG=1")
	}

	SetDebugMode(true)
	if !IsDebugMode() {
		t.Fatal("expected /debug on to force enable")
	}
}

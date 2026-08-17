// internal/agent/fuzz_test.go
// Purpose: Phase 7 (P7.5) — fuzz the transcript→policy parsers (deictic vision
// detection and the undo intent matcher). Invariant: pure functions, never
// panic, boolean result only.
package agent

import "testing"

func FuzzTranscriptPolicyParsers(f *testing.F) {
	f.Add("what's wrong with this code")
	f.Add("undo that")
	f.Add("run the tests")
	f.Add("")

	f.Fuzz(func(t *testing.T, text string) {
		// Both are deterministic boolean classifiers; exercising them with
		// arbitrary transcript text must never panic.
		_ = isDeictic(text)
		_ = isUndoIntent(text)
	})
}

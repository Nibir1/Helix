// internal/shell/fuzz_test.go
// Purpose: Continuous fuzzing for the unified input classifier.
// Invariants: Confidence ∈ [0,1]; '/'-prefixed input always KindSlashCommand.
package shell

import (
	"strings"
	"testing"
)

func FuzzClassify(f *testing.F) {
	seeds := []string{
		"ls -la",
		"/git undo last commit",
		"what is a process",
		"",
		"   ",
		"find . -name *.go",
		"find all large files",
		"echo hello | grep world",
		"/usr/bin/echo hi",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		c := Classify(input)

		// Invariant 1: Confidence must be between 0.0 and 1.0.
		if c.Confidence < 0.0 || c.Confidence > 1.0 {
			t.Fatalf("confidence out of bounds [0,1]: %v", c.Confidence)
		}

		// Invariant 2: '/'-prefixed input (after trim) must always be KindSlashCommand.
		trimmed := strings.TrimSpace(input)
		if strings.HasPrefix(trimmed, "/") && c.Kind != KindSlashCommand {
			t.Fatalf("expected KindSlashCommand for '/'-prefixed input, got %v", c.Kind)
		}
	})
}

// internal/ai/planner_adapter_test.go
package ai

import (
	"strings"
	"testing"
)

func TestIsPlannerJSON(t *testing.T) {
	valid := `{"intent":"chat","steps":[{"tool":"response","message":"hi"}]}`

	if !isPlannerJSON(valid) {
		t.Fatal("expected valid JSON to pass")
	}

	fenced := "```json\n" + valid + "\n```"

	if !isPlannerJSON(fenced) {
		t.Fatal("expected fenced JSON to pass")
	}

	invalid := `{"intent":"chat","steps":[`

	if isPlannerJSON(invalid) {
		t.Fatal("expected invalid JSON to fail")
	}
}

// TestBuildMinimalPlannerPromptContainsGitHints verifies the last-resort
// prompt includes git-specific schema hints that help the model produce
// valid plans for commit/push workflows.
//
// Args:
//   - t: test runner.
//
// Returns: none.
// Complexity: O(1).
func TestBuildMinimalPlannerPromptContainsGitHints(t *testing.T) {
	prompt := BuildMinimalPlannerPrompt("commit and push", "OS: linux")
	for _, want := range []string{
		`"action":"commit"`,
		`"action":"push"`,
		`"action":"add"`,
		"OUTPUT THE COMPLETE JSON NOW",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("minimal prompt missing %q", want)
		}
	}
}

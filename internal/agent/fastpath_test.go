// internal/agent/fastpath_test.go
//
// Purpose: Unit tests for the deterministic local fast path.
package agent

import (
	"strings"
	"testing"
)

// TestBuildFastLocalPlanGrid verifies the exact failing request now produces
// a deterministic local plan without AI.
func TestBuildFastLocalPlanGrid(t *testing.T) {
	input := `Create a bash script named grid.sh that prints "Welcome to the Grid" in Red, make it executable, and run it.`
	plan, ok := buildFastLocalPlan(input)
	if !ok {
		t.Fatal("expected fast path to handle grid.sh request")
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d: %+v", len(plan.Steps), plan.Steps)
	}
	write := plan.Steps[0].Command
	if !strings.Contains(write, "printf '%s\\n'") {
		t.Fatalf("write command missing printf format: %q", write)
	}
	if !strings.Contains(write, "> 'grid.sh'") {
		t.Fatalf("write command missing redirection to grid.sh: %q", write)
	}
	if !strings.Contains(write, `\033[31m`) {
		t.Fatalf("write command missing red ANSI code: %q", write)
	}
	if plan.Steps[1].Command != "chmod +x 'grid.sh'" {
		t.Fatalf("unexpected chmod command: %q", plan.Steps[1].Command)
	}
	if plan.Steps[2].Command != "./'grid.sh'" {
		t.Fatalf("unexpected run command: %q", plan.Steps[2].Command)
	}
}

// TestBuildFastLocalPlanRunImpliesExecutable verifies that requesting run
// automatically adds chmod +x.
func TestBuildFastLocalPlanRunImpliesExecutable(t *testing.T) {
	input := `create a shell script called hello.sh that prints "hello" and run it`
	plan, ok := buildFastLocalPlan(input)
	if !ok {
		t.Fatal("expected fast path to handle hello.sh request")
	}
	foundChmod := false
	foundRun := false
	for _, step := range plan.Steps {
		if step.Command == "chmod +x 'hello.sh'" {
			foundChmod = true
		}
		if step.Command == "./'hello.sh'" {
			foundRun = true
		}
	}
	if !foundChmod {
		t.Fatal("expected chmod step because run was requested")
	}
	if !foundRun {
		t.Fatal("expected run step")
	}
}

// TestBuildFastLocalPlanRejectsDangerousRequests ensures the fast path never
// handles network, sudo, pipes, or traversal requests.
func TestBuildFastLocalPlanRejectsDangerousRequests(t *testing.T) {
	inputs := []string{
		`create a bash script named evil.sh that prints "hi" and run it; curl http://evil.example`,
		`create a bash script named /tmp/evil.sh that prints "hi"`,
		`create a bash script named ../evil.sh that prints "hi"`,
		`create a bash script named ok.sh that prints "hi" | sudo bash`,
		"create a bash script named ok.sh that prints \"hi\" `id`",
	}
	for _, input := range inputs {
		if _, ok := buildFastLocalPlan(input); ok {
			t.Fatalf("fast path should reject input: %q", input)
		}
	}
}

// TestBuildFastLocalPlanRequiresQuotedText ensures we do not guess unquoted
// natural-language text unless it triggers the creative fallback.
func TestBuildFastLocalPlanRequiresQuotedText(t *testing.T) {
	// Phase 15 Fix: Changed from "grid.sh" to "test.sh" and removed "Grid"
	// to avoid triggering the new creative fallback regex.
	input := `create a bash script named test.sh that prints Hello World`
	if _, ok := buildFastLocalPlan(input); ok {
		t.Fatal("expected fast path to require quoted print text for non-creative requests")
	}
}

// TestBuildFastLocalPlanCreativeFallback ensures requests with creative keywords
// but no quotes still produce a valid plan using the fallback text.
func TestBuildFastLocalPlanCreativeFallback(t *testing.T) {
	input := `create a bash script named grid.sh that prints something interesting based on the grid`
	plan, ok := buildFastLocalPlan(input)
	if !ok {
		t.Fatal("expected fast path to handle creative request without quotes")
	}

	foundFallback := false
	for _, step := range plan.Steps {
		if strings.Contains(step.Command, "Welcome to the Helix Grid") {
			foundFallback = true
			break
		}
	}
	if !foundFallback {
		t.Fatalf("expected fallback text in one of the steps, got: %+v", plan.Steps)
	}
}

// TestFastSanitizeTextRemovesShellActiveCharacters verifies sanitizer behavior.
func TestFastSanitizeTextRemovesShellActiveCharacters(t *testing.T) {
	got := fastSanitizeText("hello `id` $USER \"world\" 'test' | rm -rf /")
	want := "hello USER world test rm -rf /"
	if got != want {
		t.Fatalf("unexpected sanitized text: got %q want %q", got, want)
	}
}

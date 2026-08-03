// internal/ai/planner_test.go
package ai

import (
	"os"
	"testing"
)

func TestParsePlanFromModelOutput_Golden(t *testing.T) {
	raw, err := os.ReadFile("testdata/planner_multi_step.txt")
	if err != nil {
		t.Fatalf("failed to read golden file: %v", err)
	}

	plan, err := ParsePlanFromModelOutput(string(raw))
	if err != nil {
		t.Fatalf("failed to parse golden planner output: %v", err)
	}

	if plan.Intent != IntentMultiStep {
		t.Fatalf("expected multi_step intent, got %s", plan.Intent)
	}

	if len(plan.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(plan.Steps))
	}

	if plan.Steps[0].Tool != "shell" {
		t.Fatalf("expected first tool shell, got %s", plan.Steps[0].Tool)
	}

	if plan.Steps[1].Tool != "git" || plan.Steps[1].Action != "add" {
		t.Fatalf("expected second step git add, got %+v", plan.Steps[1])
	}

	if plan.Steps[1].Args["paths"] != "README.md" {
		t.Fatalf("expected paths README.md, got %q", plan.Steps[1].Args["paths"])
	}

	if plan.Steps[3].Args["name"] != "v1.1.0" {
		t.Fatalf("expected tag v1.1.0, got %q", plan.Steps[3].Args["name"])
	}
}

func TestParsePlanFromModelOutput_RejectsInvalid(t *testing.T) {
	_, err := ParsePlanFromModelOutput("this is not json")
	if err == nil {
		t.Fatal("expected error for non-JSON output")
	}
}

func TestParsePlanFromModelOutput_RejectsUnbalancedBraces(t *testing.T) {
	_, err := ParsePlanFromModelOutput(`{"intent":"chat","steps":[]`)
	if err == nil {
		t.Fatal("expected error for unbalanced braces")
	}
}

// internal/ai/planner_todo_test.go
// Purpose: the todo tool closes like every other tool, and the prompt tells the
// model the thing that makes the hybrid work.
package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func todoPlan(t *testing.T, action string, args map[string]string) (*Plan, error) {
	t.Helper()
	a, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return ParsePlanFromModelOutput(fmt.Sprintf(
		`{"intent":"chat","steps":[{"tool":"todo","action":%q,"args":%s}]}`, action, a))
}

func TestTodoActionVocabularyIsClosed(t *testing.T) {
	if _, err := todoPlan(t, "reorder", map[string]string{"id": "1"}); err == nil {
		t.Fatal("an unsupported todo action survived validation")
	}
}

func TestTodoSynonymsNormalize(t *testing.T) {
	cases := map[string]string{
		"add": "add", "create": "add",
		"revise": "revise", "rewrite": "revise", "edit": "revise",
		"state": "state", "set": "state", "status": "state",
		"drop": "drop", "remove": "drop", "delete": "drop",
	}
	for given, want := range cases {
		args := map[string]string{"id": "1", "text": "t", "state": "done"}
		p, err := todoPlan(t, given, args)
		if err != nil {
			t.Errorf("action %q was dropped: %v", given, err)
			continue
		}
		if got := p.Steps[0].Action; got != want {
			t.Errorf("action %q normalized to %q, want %q", given, got, want)
		}
	}
}

func TestTodoStepsMissingRequiredArgsAreDropped(t *testing.T) {
	cases := []struct {
		action string
		args   map[string]string
		why    string
	}{
		{"add", map[string]string{}, "no text"},
		{"revise", map[string]string{"text": "x"}, "no id"},
		{"revise", map[string]string{"id": "1"}, "no text"},
		{"state", map[string]string{"id": "1"}, "no state"},
		{"state", map[string]string{"state": "done"}, "no id"},
		{"drop", map[string]string{}, "no id"},
	}
	for _, c := range cases {
		if _, err := todoPlan(t, c.action, c.args); err == nil {
			t.Errorf("todo/%s with %s survived validation", c.action, c.why)
		}
	}
}

// A todo step must never carry a command — otherwise the cheapest-gated tool in
// the vocabulary becomes a second door into shell execution.
func TestTodoStepNeverCarriesACommand(t *testing.T) {
	p, err := ParsePlanFromModelOutput(
		`{"intent":"chat","steps":[{"tool":"todo","action":"add","args":{"text":"x"},"command":"rm -rf /"}]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Steps[0].Command != "" {
		t.Errorf("a todo step kept a command: %q", p.Steps[0].Command)
	}
}

// The hybrid only works if the model is told three things: that the list can be
// wrong, that it may change it, and what it may not do.
func TestPromptTeachesTheHybrid(t *testing.T) {
	prompt := BuildPlannerPrompt("do a thing", "", "")
	lower := strings.ToLower(prompt)

	checks := []struct{ needle, why string }{
		{"can be wrong", "the model is not told the human's plan may be wrong, so it will work around a bad task instead of fixing it"},
		{"a reason is required", "the model is not told an overrule needs a reason"},
		{"never deleted", "the model is not told user tasks cannot be deleted, so it will try and get an error mid-run"},
		{"one task \"in_progress\"", "the single-in-progress rule is not stated"},
		{"#id [state] (by you|by helix)", "the model is not told how to read the list, so it cannot address an item"},
		// The first real run closed "run the tests" on a test run that had
		// happened BEFORE the version bump — honestly reported ("already ran in
		// the previous plan") and wrong, because the edit after it was the one
		// nobody checked.
		{"after the last change you made", "nothing stops the model closing a verification task on stale evidence"},
		{"run them again before closing", "the model is not told the remedy, only the rule"},
		// A real run edited version.go successfully and then marked the task
		// SUPERSEDED because "the bump it asks for is already in place" — it
		// having been the one that put it there. Superseded reads as "this was
		// never needed", which is the opposite of what happened.
		{"if you did the work, the task is \"done\"", "nothing stops a task being superseded after its own work was done"},
	}
	for _, c := range checks {
		if !strings.Contains(lower, strings.ToLower(c.needle)) {
			t.Errorf("planner prompt is missing %q — %s", c.needle, c.why)
		}
	}
}

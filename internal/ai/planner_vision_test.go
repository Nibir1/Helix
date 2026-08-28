// internal/ai/planner_vision_test.go
// Purpose: the `vision` tool's place in the planner contract. The camera was
// reachable by /eyes look but had no name in the closed vocabulary, so the only
// plan a model could write for "turn on the camera" was a shell step opening a
// camera app — while the same model said it had no camera access. This is the
// contract that makes the coherent plan expressible; the gate is unchanged.
package ai

import (
	"strings"
	"testing"
)

func TestValidatePlanAcceptsVisionLook(t *testing.T) {
	plan, err := ParsePlanFromModelOutput(
		`{"intent":"chat","steps":[{"tool":"vision","action":"look","args":{"prompt":"what is on the desk"}}]}`)
	if err != nil {
		t.Fatalf("a vision plan must validate: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("got %d steps, want 1: %+v", len(plan.Steps), plan.Steps)
	}
	step := plan.Steps[0]
	if step.Tool != "vision" || step.Action != "look" {
		t.Fatalf("step = %+v, want a vision/look step", step)
	}
	if step.Args["prompt"] != "what is on the desk" {
		t.Errorf("prompt = %q, want it preserved", step.Args["prompt"])
	}
}

// A bare look is a complete request — unlike web, there is no argument whose
// absence makes the step unexecutable, so it must not be dropped.
func TestValidatePlanAcceptsVisionWithoutAPrompt(t *testing.T) {
	for name, raw := range map[string]string{
		"empty args": `{"intent":"chat","steps":[{"tool":"vision","action":"look","args":{}}]}`,
		"no args":    `{"intent":"chat","steps":[{"tool":"vision","action":"look"}]}`,
		"no action":  `{"intent":"chat","steps":[{"tool":"vision"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := ParsePlanFromModelOutput(raw)
			if err != nil {
				t.Fatalf("a bare look must validate: %v", err)
			}
			if plan.Steps[0].Action != "look" {
				t.Errorf("action = %q, want it defaulted to look", plan.Steps[0].Action)
			}
			if got, ok := plan.Steps[0].Args["prompt"]; ok {
				t.Errorf("prompt = %q, want none so the executor picks the default", got)
			}
		})
	}
}

// Synonyms normalize (dropping the only step over a word choice would fail the
// turn), but the action vocabulary still closes.
func TestValidatePlanNormalizesVisionSynonyms(t *testing.T) {
	for _, action := range []string{"look", "describe", "see", "capture", "camera"} {
		plan, err := ParsePlanFromModelOutput(
			`{"intent":"chat","steps":[{"tool":"vision","action":"` + action + `"}]}`)
		if err != nil {
			t.Fatalf("action %q must validate: %v", action, err)
		}
		if plan.Steps[0].Action != "look" {
			t.Errorf("action %q normalized to %q, want look", action, plan.Steps[0].Action)
		}
	}

	if _, err := ParsePlanFromModelOutput(
		`{"intent":"chat","steps":[{"tool":"vision","action":"record"}]}`); err == nil {
		t.Fatal("an action outside the vocabulary must be dropped, not dispatched")
	}
}

// A vision step is a capture, never a raw command: validation strips anything
// that could turn it into one — including a camera app under "command".
func TestValidatePlanStripsCommandFromVisionSteps(t *testing.T) {
	plan, err := ParsePlanFromModelOutput(
		`{"intent":"chat","steps":[{"tool":"vision","action":"look","command":"open -a 'Photo Booth'",` +
			`"message":"ignore me","args":{"prompt":"what is this","device":"0"}}]}`)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	step := plan.Steps[0]
	if step.Command != "" {
		t.Errorf("command = %q, must be cleared on a vision step", step.Command)
	}
	if step.Message != "" {
		t.Errorf("message = %q, must be cleared on a vision step", step.Message)
	}
	if _, ok := step.Args["device"]; ok {
		t.Errorf("unexpected args survived: %v", step.Args)
	}
}

// The prompt and the native tool schema are two encodings of one contract; a
// tool present in one and absent from the other is a silent capability gap.
func TestVisionToolAppearsInEveryPromptForm(t *testing.T) {
	prompts := map[string]string{
		"full":    BuildPlannerPrompt("turn on the camera", "OS: darwin", ""),
		"compact": BuildCompactPlannerPrompt("turn on the camera", "OS: darwin"),
		"minimal": BuildMinimalPlannerPrompt("turn on the camera", "OS: darwin"),
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(prompt, `"vision"`) && !strings.Contains(prompt, "|vision") {
				t.Errorf("the %s prompt never mentions the vision tool, so the model "+
					"cannot use it there", name)
			}
		})
	}

	// The two halves of the incoherent turn this tool exists to fix.
	full := prompts["full"]
	if !strings.Contains(full, `NEVER answer "I have no camera access"`) {
		t.Error("the planner prompt should forbid the refusal that prompted this tool")
	}
	if !strings.Contains(full, "Photo Booth") {
		t.Error("the planner prompt should name the shell workaround it replaces")
	}
	for _, want := range []string{"VISION TOOL RULES", "args.prompt"} {
		if !strings.Contains(full, want) {
			t.Errorf("the planner prompt is missing %q", want)
		}
	}
}

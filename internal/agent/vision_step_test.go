// internal/agent/vision_step_test.go
// Purpose: the planner's `vision` step, end to end through executePlanSteps
// with fake seams. The camera worked and the planner could not reach it, so
// what matters here is that a planned step lands on the SAME capture path as
// /eyes look, and that an unavailable camera fails the step with an
// actionable message instead of a shell workaround.
package agent

import (
	"context"
	"strings"
	"testing"

	"helix/internal/ai"
)

// wireFakeVision installs seams that record what they were asked for.
func wireFakeVision(ag *Agent, answer string) (frames *int, prompt *string) {
	frames, prompt = new(int), new(string)
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) {
		*frames++
		return []byte("fake-frame"), nil
	}
	ag.VisionCall = func(p string, _ []byte) (string, error) {
		*prompt = p
		return answer, nil
	}
	return frames, prompt
}

func TestVisionStepCapturesOneFrameAndAnswers(t *testing.T) {
	ag, spoken := newTestAgent(t)
	frames, prompt := wireFakeVision(ag, "a mug and a keyboard")

	plan := &ai.Plan{Intent: ai.IntentChat, Steps: []ai.PlanStep{
		{Tool: "vision", Action: "look", Args: map[string]string{"prompt": "what is on the desk"}},
	}}
	obs := ag.executePlanSteps(plan, map[string]bool{})

	if len(obs) != 1 || !obs[0].OK {
		t.Fatalf("observations = %+v, want one successful step", obs)
	}
	if *frames != 1 {
		t.Errorf("captured %d frames, want exactly one per turn", *frames)
	}
	// The persona wraps it, but the step's own question must survive intact.
	if !strings.Contains(*prompt, "what is on the desk") {
		t.Errorf("the step's question did not reach the model:\n%s", *prompt)
	}
	if !strings.Contains(*prompt, "You are Helix") {
		t.Error("the camera path must carry the persona, or it answers in catalogue prose")
	}
	// The description is available to a replan…
	if obs[0].Output != "a mug and a keyboard" {
		t.Errorf("output = %q, want the description recorded for the harness", obs[0].Output)
	}
	// …but the step already answered the user, so the harness must not be told
	// to produce a second reply about it.
	if obs[0].NeedsAnswer {
		t.Error("a vision step must not set NeedsAnswer: it has already spoken its answer")
	}
	if len(*spoken) == 0 || (*spoken)[len(*spoken)-1] != "a mug and a keyboard" {
		t.Errorf("spoken = %v, want the description", *spoken)
	}
}

// A bare look is a valid plan; the default question comes from the shared
// implementation, not from each caller.
func TestVisionStepWithoutPromptUsesTheSharedDefault(t *testing.T) {
	ag, _ := newTestAgent(t)
	_, prompt := wireFakeVision(ag, "a wall")

	if _, err := ag.handleVisionStep(ai.PlanStep{Tool: "vision", Action: "look"}); err != nil {
		t.Fatalf("a bare look must run: %v", err)
	}
	if !strings.Contains(strings.ToLower(*prompt), "what do you see") {
		t.Errorf("the shared default question did not reach the model:\n%s", *prompt)
	}
}

// The whole point of the tool: an unavailable camera says so, in terms the user
// can act on. Before this the planner reached for `open -a 'Photo Booth'`.
func TestVisionStepFailsLoudlyWhenEyesAreOff(t *testing.T) {
	ag, _ := newTestAgent(t)
	wireFakeVision(ag, "unreachable")
	ag.VisionEnabled = func() bool { return false }
	ag.VisionCapture = func(context.Context) ([]byte, error) {
		t.Fatal("no frame may be captured while eyes are off")
		return nil, nil
	}

	plan := &ai.Plan{Intent: ai.IntentChat, Steps: []ai.PlanStep{{Tool: "vision", Action: "look"}}}
	obs := ag.executePlanSteps(plan, map[string]bool{})

	if len(obs) != 1 || obs[0].OK {
		t.Fatalf("observations = %+v, want one failed step", obs)
	}
	if !strings.Contains(obs[0].Err, "/blackbox eyes on") {
		t.Errorf("error = %q, want it to name the command that fixes it", obs[0].Err)
	}
}

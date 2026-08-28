// internal/agent/vision.go
// Purpose: camera perception — capture one frame and answer about it.
//
// There are exactly two ways in, and both are explicit: /blackbox look from the
// keyboard, and the planner's `vision` tool. A third used to exist — any spoken
// sentence containing "this"/"that"/"here" was routed here before the planner
// saw it — and it was removed after QA showed it swallowing most of a session
// (see the note in policy_voice.go). Guessing at intent from demonstratives is
// strictly worse than letting a model pick from its own tools.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"helix/internal/ai"
)

// VisionAvailable reports whether a frame could be captured and understood right
// now: opted in, seams wired, and a vision-capable model selected.
func (a *Agent) VisionAvailable() bool {
	return a != nil && a.VisionEnabled != nil && a.VisionEnabled() &&
		a.VisionCapture != nil && a.VisionCall != nil
}

// DescribeFrame captures one frame and answers a question about it, printing and
// speaking the result like any other reply.
//
// Exported so /eyes look works from the keyboard. It shares handleVisionTurn's
// implementation, so the typed and spoken paths cannot diverge in what they
// capture, how long they wait, or what they do with the frame (memory only,
// never written to disk).
func (a *Agent) DescribeFrame(prompt string) error {
	if !a.VisionAvailable() {
		return fmt.Errorf("vision is not available (enable it with /blackbox eyes on)")
	}
	_, err := a.visionTurn(prompt)
	return err
}

// handleVisionStep executes a planner "vision" step: one frame, one answer.
//
// This is the tool the planner was missing. Without it, "turn on the camera"
// could only be expressed as a shell step — the model would open a camera app
// while simultaneously saying it had no camera access, and both halves were
// accurate about the vocabulary it had been given.
//
// The description is returned so an agentic replan can see what Helix saw, but
// the step deliberately does NOT set NeedsAnswer: visionTurn has already
// printed and spoken the answer, and a second pass would only repeat it.
//
// Args: step: a validated vision step; args.prompt is optional.
// Returns: the model's description, or an error the turn reports.
// Complexity: one capture + one vision round trip.
func (a *Agent) handleVisionStep(step ai.PlanStep) (string, error) {
	if !a.VisionAvailable() {
		// Two causes, one message: the agent cannot tell "eyes off" from "the
		// selected model cannot see" — VisionEnabled folds both in — so it
		// names the check that distinguishes them rather than guessing.
		return "", errors.New("camera vision is unavailable — turn it on with /blackbox eyes on, " +
			"and see /blackbox status for whether the selected model can see")
	}
	return a.visionTurn(step.Args["prompt"])
}

// visionTurn is the single implementation behind both the conversational vision
// path and /blackbox look. Failures degrade to a spoken notice — never a panic and
// never a silent frame.
func (a *Agent) visionTurn(prompt string) (string, error) {
	// Defaulted here rather than in each caller so the typed, spoken, and
	// planned routes cannot diverge on what a bare "look" asks the model.
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "What do you see?"
	}
	// The persona matters more here than anywhere. Handed a frame and no voice,
	// a vision model writes gallery-catalogue prose — "the person is visible
	// mainly as a dark silhouette from the shoulders up" — which is accurate,
	// unspeakable, and not how someone in the room would answer.
	prompt = PersonaPrompt(true, "you are looking through this machine's camera right now") +
		"Answer about what the camera sees.\n\n" + prompt

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// §10 frame-to-insight latency (≤5s best-effort on llava): from frame
	// capture start to the vision model returning its answer.
	start := time.Now()
	frame, err := a.VisionCapture(ctx)
	if err != nil {
		a.render.PrintWarning(fmt.Sprintf("Vision capture failed: %v", err))
		a.speak("I could not access the camera.")
		return "", fmt.Errorf("capture: %w", err)
	}

	resp, err := a.VisionCall(prompt, frame)
	if err != nil {
		a.render.PrintWarning(fmt.Sprintf("Vision failed: %v", err))
		a.speak("I could not analyze what I saw.")
		return "", fmt.Errorf("vision model: %w", err)
	}

	if a.OnVisionMetric != nil {
		a.OnVisionMetric("frame_to_insight", time.Since(start))
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		a.render.PrintWarning("The vision model returned nothing.")
		return "", fmt.Errorf("vision model returned an empty answer")
	}
	a.render.PrintAIMessage(resp, a.typingEffect)
	a.lastResponse = resp
	a.speak(resp)
	return resp, nil
}

// internal/agent/vision.go
// Purpose: BlackBox Phase 5 conversational wiring — in voice mode with /eyes
// on, a deictic utterance ("what's wrong with THIS code?") captures one frame
// and routes prompt+frame through the vision seam instead of the text
// planner. Non-deictic queries are unaffected; one frame per turn.
package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helix/internal/input"
)

// deicticMarkers are words that indicate the utterance refers to something the
// user can see. Deliberately conservative: a false positive costs one captured
// frame (memory-only), a false negative just falls through to the text path.
var deicticMarkers = []string{
	"this", "that", "here", "these", "those", "screen", "look at", "looking at",
	"read this", "what do you see", "what's on screen", "what is on screen",
}

// isDeictic reports whether the utterance likely references visible content.
func isDeictic(text string) bool {
	lower := strings.ToLower(text)
	for _, m := range deicticMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// visionRequested reports whether this turn should route to the vision path:
// voice channel + eyes enabled + seams wired + deictic utterance.
//
// The voice-channel condition stays, deliberately. It looks like an arbitrary
// restriction — and it did make the camera unreachable from the keyboard, which
// was a real gap — but it is doing necessary work: deictic words are ambiguous
// in TYPED input in a way they are not when spoken. "what does this do?" typed
// at a terminal almost always means the text on screen, and firing the camera on
// it would be a privacy surprise on the most ordinary phrasing there is. Spoken
// at a machine with the camera opted in, the same words usually do mean
// something visible.
//
// The keyboard gap is closed by an EXPLICIT entry point instead — /eyes look,
// which routes through DescribeFrame. Explicit beats guessing when a camera is
// the thing being guessed about.
func (a *Agent) visionRequested(ev input.InputEvent) bool {
	return a.channel == input.ChannelVoice &&
		a.VisionEnabled != nil && a.VisionEnabled() &&
		a.VisionCapture != nil && a.VisionCall != nil &&
		isDeictic(ev.Text)
}

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
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "Describe what you see, briefly and concretely."
	}
	if !a.VisionAvailable() {
		return fmt.Errorf("vision is not available (enable it with /eyes on)")
	}
	return a.visionTurn(prompt)
}

// handleVisionTurn captures one frame and answers the utterance through the
// vision model. Failures degrade to a spoken notice — never a panic or a
// silent frame.
func (a *Agent) handleVisionTurn(ev input.InputEvent) {
	_ = a.visionTurn(ev.Text)
}

// visionTurn is the single implementation behind both the conversational vision
// path and /eyes look. Failures degrade to a spoken notice — never a panic and
// never a silent frame.
func (a *Agent) visionTurn(prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// §10 frame-to-insight latency (≤5s best-effort on llava): from frame
	// capture start to the vision model returning its answer.
	start := time.Now()
	frame, err := a.VisionCapture(ctx)
	if err != nil {
		a.render.PrintWarning(fmt.Sprintf("Vision capture failed: %v", err))
		a.speak("I could not access the camera.")
		return fmt.Errorf("capture: %w", err)
	}

	resp, err := a.VisionCall(prompt, frame)
	if err != nil {
		a.render.PrintWarning(fmt.Sprintf("Vision failed: %v", err))
		a.speak("I could not analyze what I saw.")
		return fmt.Errorf("vision model: %w", err)
	}

	if a.OnVisionMetric != nil {
		a.OnVisionMetric("frame_to_insight", time.Since(start))
	}

	resp = strings.TrimSpace(resp)
	if resp == "" {
		a.render.PrintWarning("The vision model returned nothing.")
		return fmt.Errorf("vision model returned an empty answer")
	}
	a.render.PrintAIMessage(resp, a.typingEffect)
	a.lastResponse = resp
	a.speak(resp)
	return nil
}

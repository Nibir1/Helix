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
func (a *Agent) visionRequested(ev input.InputEvent) bool {
	return a.channel == input.ChannelVoice &&
		a.VisionEnabled != nil && a.VisionEnabled() &&
		a.VisionCapture != nil && a.VisionCall != nil &&
		isDeictic(ev.Text)
}

// handleVisionTurn captures one frame and answers the utterance through the
// vision model. Failures degrade to a spoken notice — never a panic or a
// silent frame.
func (a *Agent) handleVisionTurn(ev input.InputEvent) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// §10 frame-to-insight latency (≤5s best-effort on llava): from frame
	// capture start to the vision model returning its answer.
	start := time.Now()
	frame, err := a.VisionCapture(ctx)
	if err != nil {
		a.render.PrintWarning(fmt.Sprintf("Vision capture failed: %v", err))
		a.speak("I could not access the camera.")
		return
	}

	resp, err := a.VisionCall(ev.Text, frame)
	if err != nil {
		a.render.PrintWarning(fmt.Sprintf("Vision failed: %v", err))
		a.speak("I could not analyze what I saw.")
		return
	}

	if a.OnVisionMetric != nil {
		a.OnVisionMetric("frame_to_insight", time.Since(start))
	}

	resp = strings.TrimSpace(resp)
	a.render.PrintAIMessage(resp, a.typingEffect)
	a.lastResponse = resp
	a.speak(resp)
}

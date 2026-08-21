// internal/agent/vision_test.go
// Purpose: Phase 5 routing proof without a camera or model — fake vision seams
// injected into the agent, driven by synthetic voice InputEvents (roadmap §9).
package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"helix/internal/input"
)

func TestIsDeictic(t *testing.T) {
	cases := map[string]bool{
		"what's wrong with this code": true,
		"read this serial number":     true,
		"what do you see":             true,
		"look at that error":          true,
		"run the test suite":          false,
		"list the files":              false,
		"why is my build failing":     false,
		"":                            false,
	}
	for text, want := range cases {
		if got := isDeictic(text); got != want {
			t.Errorf("isDeictic(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestVisionRoutingCapturesAndSpeaks(t *testing.T) {
	ag, spoken := newTestAgent(t)

	var captured []byte
	var promptSeen string
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) {
		captured = []byte("fake-frame")
		return captured, nil
	}
	ag.VisionCall = func(prompt string, _ []byte) (string, error) {
		promptSeen = prompt
		return "the build fails on a missing semicolon", nil
	}

	ag.HandleInputEvent(input.InputEvent{Text: "what's wrong with this code", Channel: input.ChannelVoice})

	if string(captured) != "fake-frame" {
		t.Fatalf("frame not captured: %q", captured)
	}
	if promptSeen != "what's wrong with this code" {
		t.Fatalf("prompt not routed to vision: %q", promptSeen)
	}
	if len(*spoken) == 0 || (*spoken)[len(*spoken)-1] != "the build fails on a missing semicolon" {
		t.Fatalf("vision response must be spoken, got %v", *spoken)
	}
}

func TestVisionNotRequestedWhenOffOrTyped(t *testing.T) {
	ag, _ := newTestAgent(t)

	// Eyes off → never routes, even for deictic voice input.
	ag.VisionEnabled = func() bool { return false }
	ag.VisionCapture = func(context.Context) ([]byte, error) { t.Fatal("capture must not run"); return nil, nil }
	ag.VisionCall = func(string, []byte) (string, error) { t.Fatal("call must not run"); return "", nil }

	if ag.visionRequested(input.InputEvent{Text: "what's wrong with this", Channel: input.ChannelVoice}) {
		t.Fatal("vision must not be requested while eyes are off")
	}

	// Typed input never routes to vision, even with eyes on.
	ag.VisionEnabled = func() bool { return true }
	if ag.visionRequested(input.InputEvent{Text: "what's wrong with this", Channel: input.ChannelText}) {
		t.Fatal("typed input must never route to vision")
	}

	// Non-deictic voice input with eyes on still skips vision.
	if ag.visionRequested(input.InputEvent{Text: "run the tests", Channel: input.ChannelVoice}) {
		t.Fatal("non-deictic input must not route to vision")
	}
}

func TestVisionCaptureFailureDegradesGracefully(t *testing.T) {
	ag, spoken := newTestAgent(t)
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) { return nil, errors.New("no camera") }
	ag.VisionCall = func(string, []byte) (string, error) {
		t.Fatal("call must not run after capture failure")
		return "", nil
	}

	ag.HandleInputEvent(input.InputEvent{Text: "look at this", Channel: input.ChannelVoice})

	if len(*spoken) == 0 {
		t.Fatal("capture failure must be spoken (no silent frame drop)")
	}
}

// TestVisionOnMetricFiresAfterInsight proves the §10 frame-to-insight seam:
// a successful vision turn reports a positive latency sample; failures do not.
func TestVisionOnMetricFiresAfterInsight(t *testing.T) {
	ag, _ := newTestAgent(t)
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) { return []byte("frame"), nil }
	ag.VisionCall = func(string, []byte) (string, error) { return "insight", nil }

	var metric string
	var latency time.Duration
	ag.OnVisionMetric = func(m string, d time.Duration) {
		metric, latency = m, d
	}

	ag.HandleInputEvent(input.InputEvent{Text: "what do you see", Channel: input.ChannelVoice})

	if metric != "frame_to_insight" {
		t.Fatalf("metric name = %q, want frame_to_insight", metric)
	}
	if latency <= 0 {
		t.Fatalf("latency = %v, want > 0", latency)
	}

	// Failure path must not emit a metric (no bogus sample on degraded turns).
	metric = ""
	ag.VisionCapture = func(context.Context) ([]byte, error) { return nil, errors.New("no camera") }
	ag.HandleInputEvent(input.InputEvent{Text: "look at this", Channel: input.ChannelVoice})
	if metric != "" {
		t.Fatalf("metric fired on capture failure: %q", metric)
	}
}

// TestDescribeFrameIsTheKeyboardPath covers the explicit entry point that closes
// the keyboard gap without making typed deixis fire the camera.
//
// The two halves belong together: visionRequested deliberately ignores typed
// input (a typed "what does this do?" means the screen, not the room), so the
// only way a typed session reaches the camera is by asking for it outright.
func TestDescribeFrameIsTheKeyboardPath(t *testing.T) {
	ag, spoken := newTestAgent(t)

	var gotPrompt string
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) { return []byte{0xFF, 0xD8}, nil }
	ag.VisionCall = func(prompt string, frame []byte) (string, error) {
		gotPrompt = prompt
		return "a terminal window", nil
	}

	if err := ag.DescribeFrame("what is on the screen"); err != nil {
		t.Fatalf("DescribeFrame: %v", err)
	}
	if gotPrompt != "what is on the screen" {
		t.Errorf("prompt = %q, want the caller's question", gotPrompt)
	}
	if len(*spoken) == 0 {
		t.Error("the answer should be spoken as well as printed")
	}

	// No question supplied → a sensible default rather than an empty prompt,
	// which some vision models answer with nothing at all.
	if err := ag.DescribeFrame("   "); err != nil {
		t.Fatalf("DescribeFrame with no question: %v", err)
	}
	if gotPrompt == "" {
		t.Error("an empty question must be replaced with a default prompt")
	}
}

func TestDescribeFrameRefusesWhenUnavailable(t *testing.T) {
	ag, _ := newTestAgent(t)

	// Eyes off.
	ag.VisionEnabled = func() bool { return false }
	ag.VisionCapture = func(context.Context) ([]byte, error) { t.Fatal("must not capture"); return nil, nil }
	ag.VisionCall = func(string, []byte) (string, error) { t.Fatal("must not call"); return "", nil }
	if err := ag.DescribeFrame("look"); err == nil {
		t.Error("DescribeFrame must refuse while eyes are off")
	}

	// Seams not wired (headless build, no camera support compiled in).
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = nil
	if ag.VisionAvailable() {
		t.Error("VisionAvailable must be false without a capture seam")
	}
	if err := ag.DescribeFrame("look"); err == nil {
		t.Error("DescribeFrame must refuse without a capture seam")
	}
}

// TestVisionTurnReportsEmptyAnswers: a vision model that returns nothing is a
// failure to surface, not a blank reply to print.
func TestVisionTurnReportsEmptyAnswers(t *testing.T) {
	ag, _ := newTestAgent(t)
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) { return []byte{0xFF}, nil }
	ag.VisionCall = func(string, []byte) (string, error) { return "   ", nil }

	if err := ag.DescribeFrame("what is this"); err == nil {
		t.Error("an empty vision answer must be reported as an error")
	}
}

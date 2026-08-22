// internal/agent/vision_test.go
// Purpose: Phase 5 routing proof without a camera or model — fake vision seams
// injected into the agent, driven by synthetic voice InputEvents (roadmap §9).
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestVisionCaptureFailureDegradesGracefully(t *testing.T) {
	ag, spoken := newTestAgent(t)
	ag.VisionEnabled = func() bool { return true }
	ag.VisionCapture = func(context.Context) ([]byte, error) { return nil, errors.New("no camera") }
	ag.VisionCall = func(string, []byte) (string, error) {
		t.Fatal("call must not run after capture failure")
		return "", nil
	}

	_ = ag.DescribeFrame("look at this")

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

	_ = ag.DescribeFrame("what do you see")

	if metric != "frame_to_insight" {
		t.Fatalf("metric name = %q, want frame_to_insight", metric)
	}
	if latency <= 0 {
		t.Fatalf("latency = %v, want > 0", latency)
	}

	// Failure path must not emit a metric (no bogus sample on degraded turns).
	metric = ""
	ag.VisionCapture = func(context.Context) ([]byte, error) { return nil, errors.New("no camera") }
	_ = ag.DescribeFrame("look at this")
	if metric != "" {
		t.Fatalf("metric fired on capture failure: %q", metric)
	}
}

// TestDescribeFrameIsTheKeyboardPath covers the explicit entry point.
//
// Since the deictic pre-empt was removed there are exactly two doors to the
// camera, and this is one of them: asking for it outright. The other is the
// planner choosing its `vision` tool (see vision_step_test.go).
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
	if !strings.Contains(gotPrompt, "what is on the screen") {
		t.Errorf("the caller's question did not reach the model:\n%s", gotPrompt)
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

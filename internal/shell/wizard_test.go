// internal/shell/wizard_test.go
// Purpose: a wizard step must stay inside the frame it opened, and a command a
// user is meant to copy must survive rendering intact.
package shell

import (
	"strings"
	"testing"
)

// behindTheGutter is the invariant every panel body line shares: it starts with
// the gutter and it does not run past the rule.
func behindTheGutter(t *testing.T, label string, lines []string) {
	t.Helper()
	for i, l := range lines {
		plain := Plain(l)
		if !strings.HasPrefix(plain, "  "+glyphGutter+" ") {
			t.Errorf("%s line %d escaped the gutter: %q", label, i, plain)
		}
		if visibleWidth(plain) > panelWidth()+2 {
			t.Errorf("%s line %d is %d cells, wider than the %d-cell panel: %q",
				label, i, visibleWidth(plain), panelWidth(), plain)
		}
	}
}

// A step wide enough to wrap must still be one block, not a subject inside the
// frame and a detail that restarts at column zero.
func TestStepWrapsInsideTheGutter(t *testing.T) {
	long := "port 8080 is already claimed by another service on this machine, " +
		"so whisper-local cannot bind it and has been moved elsewhere"
	out := Step(StateWarn, "whisper-local", long)
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		t.Fatalf("a %d-char detail did not wrap: %q", len(long), out)
	}
	behindTheGutter(t, "Step", lines)

	// Continuations hang under the SUBJECT, past the glyph column, or a wrapped
	// step reads as two steps.
	if !strings.HasPrefix(Plain(lines[1]), "  "+glyphGutter+"   ") {
		t.Errorf("continuation is not indented past the glyph: %q", Plain(lines[1]))
	}
}

// A short step is one line and keeps the state's glyph, so the column can be
// skimmed without reading a word.
func TestStepCarriesItsStateGlyph(t *testing.T) {
	for _, tc := range []struct {
		state State
		glyph string
	}{
		{StateGood, glyphOK},
		{StateWarn, glyphWarn},
		{StateBad, glyphBad},
		{StateIdle, glyphBullet},
	} {
		got := Plain(Step(tc.state, "piper-local", "verified"))
		if strings.Contains(got, "\n") {
			t.Errorf("a short step must be one line, got %q", got)
		}
		if !strings.Contains(got, tc.glyph) {
			t.Errorf("step is missing its %q glyph: %q", tc.glyph, got)
		}
		if !strings.Contains(got, "piper-local") || !strings.Contains(got, "verified") {
			t.Errorf("step lost its subject or detail: %q", got)
		}
	}
}

// StepDetail is subordinate to the step above it. Rendered flush with the
// gutter it reads as an independent event, which is how a connection error came
// to look like a second thing that happened rather than the reason the first
// one failed.
func TestStepDetailIsIndentedUnderItsStep(t *testing.T) {
	lines := StepDetail("whisper-local at http://127.0.0.1:8080: nothing is listening.", Muted)
	if len(lines) == 0 {
		t.Fatal("no detail lines")
	}
	behindTheGutter(t, "StepDetail", lines)
	for i, l := range lines {
		if !strings.HasPrefix(Plain(l), "  "+glyphGutter+"   ") {
			t.Errorf("detail line %d is not indented: %q", i, Plain(l))
		}
	}
}

// The one thing in a panel allowed to run wide, and it has to be: a launch
// command with a line break in it cannot be pasted into a shell. This is the
// defect that showed up the moment the screen was actually rendered — a piper
// command broken across two lines by the word wrapper.
func TestStepCommandIsNeverWrappedOrTruncated(t *testing.T) {
	cmd := "python3 -m piper.http_server --model " +
		"/Users/somebody/.helix/piper-voices/en_US-lessac-medium.onnx --port 28186"
	out := StepCommand(cmd)
	if strings.Contains(out, "\n") {
		t.Errorf("a command must not be wrapped: %q", out)
	}
	if !strings.Contains(Plain(out), cmd) {
		t.Errorf("the command did not survive rendering: %q", Plain(out))
	}
	if !strings.HasPrefix(Plain(out), "  "+glyphGutter+" ") {
		t.Errorf("a command still belongs behind the gutter: %q", Plain(out))
	}
}

// PanelWrap and StepDetail share one implementation. If they ever measure
// differently, one of them starts leaking out of the frame — which is the whole
// reason the body was extracted instead of copied.
func TestPanelWrapAndStepDetailAgreeOnWidth(t *testing.T) {
	text := strings.Repeat("a sidecar diagnosis with several words in it ", 5)
	behindTheGutter(t, "PanelWrap", PanelWrap(text, Muted))
	behindTheGutter(t, "StepDetail", StepDetail(text, Muted))
}

// Plain is what stands between a panel-styled prompt and a TTS engine reading
// the escape sequences out loud.
func TestPlainStripsEveryEscape(t *testing.T) {
	styled := Prompt("start whisper-local now", "y/N")
	got := Plain(styled)
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("Plain left an escape behind: %q", got)
	}
	if !strings.Contains(got, "start whisper-local now") {
		t.Errorf("Plain ate the question: %q", got)
	}
	if Plain("no colour here") != "no colour here" {
		t.Error("Plain must leave uncoloured text untouched")
	}
}

// internal/shell/foreign_test.go
// Purpose: the boundary between Helix's output and somebody else's.
//
// A package install printed two Helix lines, dumped pip's output at column
// zero, then printed two more. On success that is untidy; on failure a
// stranger's error text sits in the middle of Helix's own report with nothing
// saying who is talking, and the reader has to work out which lines they can
// act on.
package shell

import (
	"strings"
	"testing"
)

// The label answers the question a wall of unfamiliar text raises: who is
// talking. Naming the wrapper answers the wrong one.
func TestSourceOfNamesTheProgramNotItsWrapper(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"pip3 install --user piper-tts", "pip3", ""},
		{"/usr/local/bin/brew install whisper-cpp", "brew", "a path is not a name"},
		{"python3 -m pip install piper-tts", "pip", "a bare interpreter says nothing useful"},
		{"sudo apt-get install -y sox", "apt-get",
			"sudo granted permission; apt-get is producing the output"},
		{"sudo -u nobody apt-get install sox", "apt-get",
			"a value-taking flag must not leave the USER named as the program"},
		{"env RUSTFLAGS=-C cargo build --release", "cargo",
			"an assignment belongs to env, not to the program"},
		{"cargo build --release", "cargo", ""},
		{"sudo", "sudo", "nothing but a wrapper: name it rather than nothing"},
		{"", "the command", "an empty label reads worse than a generic one"},
	}
	for _, c := range cases {
		if got := SourceOf(c.in); got != c.want {
			msg := ""
			if c.why != "" {
				msg = " — " + c.why
			}
			t.Errorf("SourceOf(%q) = %q, want %q%s", c.in, got, c.want, msg)
		}
	}
}

// Both marks must name the source, or a long scroll cannot be matched back to
// what opened it.
func TestForeignMarksNameTheSourceAndTheVerdict(t *testing.T) {
	open := Plain(ForeignOpen("pip3"))
	if !strings.Contains(open, "pip3") {
		t.Errorf("the opening mark does not name the source: %q", open)
	}
	if !strings.Contains(open, "below") {
		t.Errorf("the opening mark does not say the output is what follows: %q", open)
	}

	ok := Plain(ForeignClose("pip3", true))
	bad := Plain(ForeignClose("brew", false))
	if !strings.Contains(ok, "pip3") || !strings.Contains(ok, "finished") {
		t.Errorf("the closing mark does not report success: %q", ok)
	}
	if !strings.Contains(bad, "brew") || !strings.Contains(bad, "failed") {
		t.Errorf("the closing mark does not report failure: %q", bad)
	}
	if ok == bad {
		t.Error("success and failure close identically; a long scroll would have to be " +
			"read backwards to find out which happened")
	}
}

// The frame must be visibly NOT the gutter Helix's own lines carry — that is
// the entire signal.
func TestForeignMarksAreNotTheHelixGutter(t *testing.T) {
	open := Plain(ForeignOpen("pip3"))
	closed := Plain(ForeignClose("pip3", true))
	own := Plain(PanelLine("a Helix line"))

	gutter := strings.TrimSpace(own)[:len(glyphGutter)]
	for _, m := range []string{open, closed} {
		if strings.HasPrefix(strings.TrimSpace(m), gutter) {
			t.Errorf("a foreign mark uses Helix's own gutter %q, so the handover is "+
				"invisible: %q", gutter, m)
		}
	}
	if strings.TrimSpace(open)[:len(glyphForeignOpen)] == strings.TrimSpace(closed)[:len(glyphForeignClose)] {
		t.Error("the opening and closing marks are the same glyph; which end you are " +
			"looking at is the thing they exist to show")
	}
}

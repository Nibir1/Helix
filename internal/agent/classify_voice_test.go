// internal/agent/classify_voice_test.go
// Purpose: Phase 2's Risks line — "transcript classification edge cases (speech
// transcripts always NL-routed — verify classifier behavior with tests)". That
// verification was never written, and the claim turned out to be false.
//
// The classifier decides on the FIRST TOKEN, so a spoken sentence whose first
// word is also an executable was classified as a shell command at full
// confidence and executed verbatim. The transcripts below are the measured
// cases, kept as the regression corpus: every one of them would have run as a
// shell line before directShellAllowed gated the bypass on channel.
package agent

import (
	"testing"

	"helix/internal/input"
	"helix/internal/shell"
)

// spokenSentencesThatLookLikeCommands are real phrasings whose first word is an
// executable. Each classifies as KindShellCommand at or above HighConfidence,
// which is what made them execute.
var spokenSentencesThatLookLikeCommands = []string{
	"make a new branch called test",
	"top three biggest directories",
	"test the code",
	"history of my commands",
	"clear the screen",
	"echo hello world",
	"date",
	"git status",
	"make test",
}

// The headline regression: none of these may bypass the planner on the voice
// channel, however confidently the classifier reads them.
func TestVoiceNeverBypassesPlannerForSpokenSentences(t *testing.T) {
	a := &Agent{channel: input.ChannelVoice}

	for _, text := range spokenSentencesThatLookLikeCommands {
		c := shell.Classify(text)
		if a.directShellAllowed(c) {
			t.Errorf("spoken %q would bypass the planner and run as a shell command "+
				"(kind=%s confidence=%.2f root=%q)",
				text, c.Kind, c.Confidence, c.RootCommand)
		}
	}
}

// The corpus has to keep earning its place. If the classifier is ever changed so
// none of these read as high-confidence shell commands any more, the test above
// would pass for the wrong reason — it would be asserting a gate on inputs the
// gate no longer sees. This fails loudly in that case so the corpus gets
// refreshed instead of quietly going hollow.
func TestSpokenSentenceCorpusStillTriggersTheClassifier(t *testing.T) {
	var triggering int
	for _, text := range spokenSentencesThatLookLikeCommands {
		c := shell.Classify(text)
		if c.Kind == shell.KindShellCommand && c.Confidence >= shell.HighConfidence {
			triggering++
		}
	}
	if triggering == 0 {
		t.Fatal("no sentence in the corpus classifies as a high-confidence shell " +
			"command any more — the regression test above is now vacuous; " +
			"refresh the corpus with current misclassifications")
	}
	t.Logf("%d/%d corpus sentences classify as high-confidence shell commands",
		triggering, len(spokenSentencesThatLookLikeCommands))
}

// Typed input must be completely unaffected. This is the guardrail half: the
// fix narrows the voice channel and must not touch the shell people type into,
// where running a confident command directly is the entire point.
func TestTypedInputStillBypassesPlanner(t *testing.T) {
	a := &Agent{channel: input.ChannelText}

	for _, cmd := range []string{"git status", "ls -la", "date", "make test", "pwd"} {
		c := shell.Classify(cmd)
		if c.Kind != shell.KindShellCommand || c.Confidence < shell.HighConfidence {
			t.Fatalf("test precondition failed: %q no longer classifies as a "+
				"high-confidence shell command (kind=%s conf=%.2f)",
				cmd, c.Kind, c.Confidence)
		}
		if !a.directShellAllowed(c) {
			t.Errorf("typed %q must still run directly — the fix must not change "+
				"the typed shell", cmd)
		}
	}
}

// Natural language must reach the planner on both channels, which was already
// true and is worth pinning next to the change.
func TestNaturalLanguageNeverBypassesPlanner(t *testing.T) {
	for _, channel := range []input.Channel{input.ChannelText, input.ChannelVoice} {
		a := &Agent{channel: channel}
		for _, text := range []string{
			"list the files in this directory",
			"show me the git status",
			"what did I ask you a moment ago",
			"who is the current user",
		} {
			if a.directShellAllowed(shell.Classify(text)) {
				t.Errorf("channel %s: %q must reach the planner", channel, text)
			}
		}
	}
}

// A low-confidence shell classification must reach the planner regardless of
// channel — that boundary predates this change and the gate must not invert it.
func TestLowConfidenceShellAlwaysReachesPlanner(t *testing.T) {
	for _, channel := range []input.Channel{input.ChannelText, input.ChannelVoice} {
		a := &Agent{channel: channel}
		c := shell.Classification{
			Kind:       shell.KindShellCommand,
			Confidence: shell.HighConfidence - 0.01,
		}
		if a.directShellAllowed(c) {
			t.Errorf("channel %s: sub-threshold confidence must reach the planner", channel)
		}
	}
}

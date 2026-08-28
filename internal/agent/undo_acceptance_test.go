// internal/agent/undo_acceptance_test.go
// Purpose: Phase 4's third acceptance criterion — "'undo that' after a
// voice-initiated `git commit` performs soft reset with confirmation."
//
// It was unchecked and it did not need hardware. What existed was
// `isUndoIntent` string matching and a proof that a FAILED commit is not
// journalled; the criterion is about the successful path, on the voice channel,
// end to end: commit → journal → spoken "undo that" → confirmation → the commit
// is actually gone from history.
//
// This is worth a real repository rather than a mock. The reversal is
// `git reset --soft HEAD~1` run through the ordinary shell pipeline, so the only
// way to prove it reverses anything is to look at the log afterwards — and the
// test that used to exercise this area is the one that accidentally committed to
// the developer's own repo, so it also matters that the git work happens strictly
// inside a temp directory.
package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/input"
)

// recordingPrompter captures what the user was asked and answers a fixed way.
type recordingPrompter struct {
	asked  []string
	answer bool
	typed  []string
}

func (p *recordingPrompter) AskYesNo(question string) bool {
	p.asked = append(p.asked, question)
	return p.answer
}
func (p *recordingPrompter) AskLine(string) string { return "" }
func (p *recordingPrompter) AskTypedConfirmation(label, _ string) bool {
	p.typed = append(p.typed, label)
	return false
}

// gitRepo builds a temp repository with one commit on top of an initial one, so
// a soft reset has somewhere to land.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not installed: %v", err)
	}
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// A hermetic identity: the developer's global config must not be
		// required, and must not be written to.
		cmd.Env = append(cmd.Environ(),
			"GIT_AUTHOR_NAME=helix-test", "GIT_AUTHOR_EMAIL=test@helix.local",
			"GIT_COMMITTER_NAME=helix-test", "GIT_COMMITTER_EMAIL=test@helix.local",
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v failed (%v): %s", args, err, out)
		}
	}

	run("init", "-q")
	run("config", "user.name", "helix-test")
	run("config", "user.email", "test@helix.local")
	if err := writeFile(dir, "seed.txt", "seed\n"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	run("add", "seed.txt")
	run("commit", "-q", "-m", "initial")
	return dir
}

func writeFile(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// gitLog returns the one-line subjects, newest first.
func gitLog(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%s")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v (%s)", err, out)
	}
	var subjects []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			subjects = append(subjects, l)
		}
	}
	return subjects
}

// The acceptance path: a journalled commit, undone by voice, with confirmation.
func TestUndoThatAfterVoiceCommitSoftResets(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)

	ag, _, undo := newTestAgentWithState(t)

	prompter := &recordingPrompter{answer: true}
	restore := commands.ActivePrompter()
	commands.SetPrompter(prompter)
	t.Cleanup(func() { commands.SetPrompter(restore) })

	// Stage a change and commit it the way a voice turn would: through the git
	// step, which is what journals the reversal.
	if err := writeFile(dir, "feature.txt", "work\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runGit(dir, "add", "feature.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}

	ag.channel = input.ChannelVoice
	if err := ag.handleGitStep(gitCommitStep("add the feature")); err != nil {
		t.Fatalf("commit step failed: %v", err)
	}
	ag.channel = input.ChannelText

	if got := gitLog(t, dir); len(got) != 2 || got[0] != "add the feature" {
		t.Fatalf("expected the new commit on top, got %v", got)
	}
	entry, ok, err := undo.Last()
	if err != nil || !ok {
		t.Fatalf("a successful commit must be journalled for undo (ok=%v err=%v)", ok, err)
	}
	if !strings.Contains(entry.ReversalCmd, "reset --soft") {
		t.Fatalf("reversal should be a soft reset, got %q", entry.ReversalCmd)
	}

	// Now the spoken "undo that".
	ag.HandleInputEvent(input.InputEvent{
		Text:    "undo that",
		Channel: input.ChannelVoice,
		Meta:    map[string]any{"stt_confidence": 0.95},
	})

	// It must have ASKED. An undo that reverses history without confirming is
	// the failure mode this criterion exists to prevent.
	if len(prompter.asked) == 0 {
		t.Fatal("undo must ask for confirmation before reversing a commit")
	}

	// And it must actually have reversed: the commit is gone, the work is not.
	after := gitLog(t, dir)
	if len(after) != 1 || after[0] != "initial" {
		t.Fatalf("soft reset did not remove the commit, log = %v", after)
	}
	if !fileExists(filepath.Join(dir, "feature.txt")) {
		t.Error("a SOFT reset must keep the working tree — the file should survive")
	}
}

// Declining the confirmation must leave history alone. Fail-closed is the whole
// point of asking.
func TestUndoDeclinedLeavesHistoryIntact(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)

	ag, _, _ := newTestAgentWithState(t)

	prompter := &recordingPrompter{answer: false} // user says no
	restore := commands.ActivePrompter()
	commands.SetPrompter(prompter)
	t.Cleanup(func() { commands.SetPrompter(restore) })

	if err := writeFile(dir, "feature.txt", "work\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runGit(dir, "add", "feature.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := ag.handleGitStep(gitCommitStep("keep me")); err != nil {
		t.Fatalf("commit step failed: %v", err)
	}

	before := gitLog(t, dir)
	ag.HandleInputEvent(input.InputEvent{Text: "undo that", Channel: input.ChannelVoice})

	if got := gitLog(t, dir); len(got) != len(before) || got[0] != before[0] {
		t.Fatalf("a declined undo must not touch history: %v → %v", before, got)
	}
}

// The reversal may run once. Journalling it twice would let a second "undo that"
// rewind a commit that was never journalled as reversible — the bug the Pop()
// fix addressed, pinned here at the acceptance level.
func TestSecondUndoDoesNotRewindAnUnjournalledCommit(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)

	ag, _, _ := newTestAgentWithState(t)
	prompter := &recordingPrompter{answer: true}
	restore := commands.ActivePrompter()
	commands.SetPrompter(prompter)
	t.Cleanup(func() { commands.SetPrompter(restore) })

	// Two commits, only the second journalled through the git step.
	if err := writeFile(dir, "a.txt", "a\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runGit(dir, "add", "a.txt"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := runGit(dir, "commit", "-q", "-m", "manual commit"); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := writeFile(dir, "b.txt", "b\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runGit(dir, "add", "b.txt"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := ag.handleGitStep(gitCommitStep("helix commit")); err != nil {
		t.Fatalf("commit step: %v", err)
	}

	ag.HandleInputEvent(input.InputEvent{Text: "undo that", Channel: input.ChannelVoice})
	afterFirst := gitLog(t, dir)

	// Second undo: there is nothing journalled, so history must not move.
	ag.HandleInputEvent(input.InputEvent{Text: "undo that", Channel: input.ChannelVoice})
	afterSecond := gitLog(t, dir)

	if len(afterSecond) != len(afterFirst) {
		t.Fatalf("a second undo rewound an unjournalled commit: %v → %v",
			afterFirst, afterSecond)
	}
	if len(afterSecond) == 0 || afterSecond[0] != "manual commit" {
		t.Fatalf("the manually-made commit must survive, log = %v", afterSecond)
	}
}

// Undo with an empty journal must say so rather than reaching for HEAD~1.
func TestUndoWithEmptyJournalDoesNothing(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)

	ag, _, _ := newTestAgentWithState(t)
	prompter := &recordingPrompter{answer: true}
	restore := commands.ActivePrompter()
	commands.SetPrompter(prompter)
	t.Cleanup(func() { commands.SetPrompter(restore) })

	before := gitLog(t, dir)
	ag.HandleInputEvent(input.InputEvent{Text: "undo that", Channel: input.ChannelVoice})

	if len(prompter.asked) != 0 {
		t.Error("with nothing journalled there is nothing to confirm")
	}
	if got := gitLog(t, dir); len(got) != len(before) {
		t.Fatalf("empty journal must not move history: %v → %v", before, got)
	}
}

// gitCommitStep builds the planner step a voice turn would produce.
func gitCommitStep(message string) ai.PlanStep {
	return ai.PlanStep{Tool: "git", Action: "commit",
		Args: map[string]string{"message": message}}
}

// runGit runs a git command in dir with a hermetic identity.
func runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=helix-test", "GIT_AUTHOR_EMAIL=test@helix.local",
		"GIT_COMMITTER_NAME=helix-test", "GIT_COMMITTER_EMAIL=test@helix.local",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, out)
	}
	return nil
}

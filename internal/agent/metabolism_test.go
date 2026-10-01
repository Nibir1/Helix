package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/metabolism"
)

func withRecorder(t *testing.T, ag *Agent) *metabolism.Recorder {
	t.Helper()
	rec, err := metabolism.Open(metabolism.Options{
		Dir:      filepath.Join(t.TempDir(), "metabolism"),
		Enabled:  true,
		Declines: commands.DeclinedConfirmations,
	})
	if err != nil {
		t.Fatal(err)
	}
	ag.Metabolism = rec
	return rec
}

func records(t *testing.T, rec *metabolism.Recorder) []metabolism.Record {
	t.Helper()
	f, err := os.Open(rec.Path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []metabolism.Record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r metabolism.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func episodes(recs []metabolism.Record) []*metabolism.Episode {
	var eps []*metabolism.Episode
	for _, r := range recs {
		if r.Episode != nil {
			eps = append(eps, r.Episode)
		}
	}
	return eps
}

// A planned turn that commits, then "undo that": the commit's episode must
// collect an "undone" outcome, and the undo itself must not become an episode.
func TestUndoIsRecordedAgainstTheEpisodeThatCommitted(t *testing.T) {
	dir := gitRepo(t)
	t.Chdir(dir)
	ag, _, undo := newTestAgentWithState(t)
	rec := withRecorder(t, ag)

	prompter := &recordingPrompter{answer: true}
	restore := commands.ActivePrompter()
	commands.SetPrompter(prompter)
	t.Cleanup(func() { commands.SetPrompter(restore) })

	if err := writeFile(dir, "feature.txt", "work\n"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(dir, "add", "feature.txt"); err != nil {
		t.Fatal(err)
	}

	// The planner half of a turn, without a model: begin, mark planned, run
	// the commit step, finish.
	ag.beginEpisode()
	commitEpisode := ag.episode.ID()
	ag.turnPlanned = true
	if err := ag.handleGitStep(gitCommitStep("add the feature")); err != nil {
		t.Fatalf("commit: %v", err)
	}
	ag.recordSteps([]StepObservation{{Tool: "git", Action: "commit", OK: true}})
	ag.finishEpisode(input.InputEvent{Text: "commit the feature"})

	entry, ok, _ := undo.Last()
	if !ok || entry.EpisodeID != commitEpisode || commitEpisode == "" {
		t.Fatalf("undo entry not linked to its episode: %+v (want %s)", entry, commitEpisode)
	}

	ag.HandleInputEvent(input.InputEvent{Text: "undo that", Channel: input.ChannelVoice,
		Meta: map[string]any{"stt_confidence": 0.95}})

	recs := records(t, rec)
	if eps := episodes(recs); len(eps) != 1 || eps[0].ID != commitEpisode {
		t.Fatalf("want exactly the commit episode recorded, got %+v", eps)
	}
	var undone bool
	for _, r := range recs {
		if r.Outcome != nil && r.Outcome.Kind == metabolism.OutcomeUndone {
			undone = r.Outcome.EpisodeID == commitEpisode
		}
	}
	if !undone {
		t.Fatalf("no undone outcome for the committing episode: %+v", recs)
	}
}

func TestOnlyPlannerTurnsAreRecorded(t *testing.T) {
	ag, _ := newTestAgent(t)
	rec := withRecorder(t, ag)

	// Not planned (a slash command, the fast path, a direct shell line).
	ag.beginEpisode()
	ag.finishEpisode(input.InputEvent{Text: "ls"})
	// Planned but a control command.
	ag.beginEpisode()
	ag.turnPlanned = true
	ag.turnWasControl = true
	ag.finishEpisode(input.InputEvent{Text: "/help"})
	ag.turnWasControl = false
	if n := len(episodes(records(t, rec))); n != 0 {
		t.Fatalf("recorded %d non-planner turns", n)
	}

	// A planned voice turn with a failed step.
	ag.beginEpisode()
	ag.turnPlanned = true
	ag.channel = input.ChannelVoice
	ag.recordSteps([]StepObservation{{Tool: "shell", Command: "go build ./...", OK: false, Err: "exit status 1"}})
	ag.finishEpisode(input.InputEvent{Text: "build it"})
	ag.channel = input.ChannelText

	eps := episodes(records(t, rec))
	if len(eps) != 1 {
		t.Fatalf("got %d episodes, want 1", len(eps))
	}
	ep := eps[0]
	if ep.End != metabolism.EndFailed || ep.Request.Provenance != metabolism.ProvUserVoice ||
		len(ep.Steps) != 1 || ep.Steps[0].Subject != "go build ./..." {
		t.Fatalf("episode: %+v", ep)
	}
}

// File steps keep their target in Args, not Command. Found in the first real
// run: "file list" was recorded with no idea of what was listed.
func TestFileStepsRecordWhatTheyTouched(t *testing.T) {
	cases := map[string]ai.PlanStep{
		"list docs":             {Tool: "file", Action: "list", Args: map[string]string{"path": "docs"}},
		"write notes.txt":       {Tool: "file", Action: "write", Args: map[string]string{"path": "notes.txt", "content": "secret body"}},
		"glob **/*.go":          {Tool: "file", Action: "glob", Args: map[string]string{"pattern": "**/*.go"}},
		"https://example.com/a": {Tool: "web", Args: map[string]string{"url": "https://example.com/a"}},
		"go test ./...":         {Tool: "shell", Command: "go test ./..."},
	}
	for want, step := range cases {
		if got := stepSubject(step); got != want {
			t.Errorf("stepSubject(%+v) = %q, want %q", step, got, want)
		}
	}
}

func TestRecordingOffLeavesNoTrace(t *testing.T) {
	ag, _ := newTestAgent(t)
	ag.beginEpisode()
	ag.turnPlanned = true
	ag.recordSteps([]StepObservation{{Tool: "shell", OK: true}})
	ag.finishEpisode(input.InputEvent{Text: "anything"}) // Metabolism is nil
	if ag.episode != nil {
		t.Fatal("episode state leaked with recording off")
	}
}

func TestRunEnds(t *testing.T) {
	cases := []struct {
		failed, open, exhausted bool
		want                    string
	}{
		{true, true, true, metabolism.EndFailed},
		{false, true, true, metabolism.EndBudgetExhausted},
		{false, true, false, metabolism.EndOpenWork},
		{false, false, true, metabolism.EndDone},
	}
	for _, c := range cases {
		if got := runEndFor(c.failed, c.open, c.exhausted); got != c.want {
			t.Errorf("runEndFor(%v,%v,%v) = %s, want %s", c.failed, c.open, c.exhausted, got, c.want)
		}
	}

	// The budget ran out mid-retrieval: the question was never answered.
	unanswered := []StepObservation{{Tool: "file", Action: "glob", OK: true, NeedsAnswer: true}}
	ag, _ := newTestAgent(t)
	ag.reportRunEnd(unanswered, 0, 3, true)
	if ag.turnEnd != metabolism.EndBudgetExhausted {
		t.Errorf("an unanswered retrieval at budget = %s, want budget-exhausted", ag.turnEnd)
	}
	answered := []StepObservation{{Tool: "file", Action: "read", OK: true, NeedsAnswer: true}, {Tool: "response", OK: true}}
	ag.reportRunEnd(answered, 0, 3, true)
	if ag.turnEnd != metabolism.EndDone {
		t.Errorf("an answered turn that used its whole budget = %s, want done", ag.turnEnd)
	}

	ag.lastObs, ag.lastResponse, ag.turnEnd = nil, "", ""
	if got := ag.derivedRunEnd(); got != metabolism.EndFailed {
		t.Errorf("no steps and no reply = %s, want failed", got)
	}
	ag.lastResponse = "here is the answer"
	if got := ag.derivedRunEnd(); got != metabolism.EndDone {
		t.Errorf("answered without steps = %s, want done", got)
	}
	ag.lastObs = []StepObservation{{OK: true}, {OK: false}}
	if got := ag.derivedRunEnd(); got != metabolism.EndFailed {
		t.Errorf("failed step = %s, want failed", got)
	}
	ag.turnEnd = metabolism.EndOpenWork
	if got := ag.derivedRunEnd(); got != metabolism.EndOpenWork {
		t.Errorf("the loop's own verdict must win, got %s", got)
	}
}

func TestDeclinedConfirmationsCountOnlyPeople(t *testing.T) {
	restore := commands.ActivePrompter()
	t.Cleanup(func() { commands.SetPrompter(restore) })

	before := commands.DeclinedConfirmations()
	commands.SetPrompter(&recordingPrompter{answer: false})
	commands.AskForConfirmation("run it?")
	if got := commands.DeclinedConfirmations() - before; got != 1 {
		t.Fatalf("a person saying no counted %d times, want 1", got)
	}
	commands.SetPrompter(unattendedPrompter{})
	commands.AskForConfirmation("run it?")
	if got := commands.DeclinedConfirmations() - before; got != 1 {
		t.Fatal("an unattended refusal was counted as a person declining")
	}
}

type unattendedPrompter struct{}

func (unattendedPrompter) AskYesNo(string) bool                     { return false }
func (unattendedPrompter) AskLine(string) string                    { return "" }
func (unattendedPrompter) AskTypedConfirmation(string, string) bool { return false }
func (unattendedPrompter) Unattended() bool                         { return true }

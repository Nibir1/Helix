package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubPlannerSequence answers with plans in order (repeating the last) and
// keeps every prompt it was given.
func stubPlannerSequence(t *testing.T, answers ...string) *[]string {
	t.Helper()
	var prompts []string
	prev := runPlanner
	runPlanner = func(prompt string) (string, error) {
		prompts = append(prompts, prompt)
		i := len(prompts) - 1
		if i >= len(answers) {
			i = len(answers) - 1
		}
		return answers[i], nil
	}
	t.Cleanup(func() { runPlanner = prev })
	return &prompts
}

const (
	readNotes   = `{"intent":"file","steps":[{"tool":"file","action":"read","args":{"path":"notes.md"}}]}`
	answerNotes = `{"intent":"chat","steps":[{"tool":"response","message":"The notes say ship on Friday."}]}`
)

// The follow-up rounds a live turn would play: the read runs, its result
// reaches the next round's prompt as the fenced execution report, and the
// replay ends when the planner answers.
func TestReplayRoundsRunsReadsAndReplans(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("ship on Friday"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompts := stubPlannerSequence(t, readNotes, answerNotes)
	ag, _ := newTestAgent(t)

	res, err := ag.ReplayRounds("what do my notes say?", []LearnedLesson{{ID: "L1", Text: "Read the named file once."}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 2 || res.End != ReplayEndAnswered || len(res.Steps) != 2 {
		t.Fatalf("result %+v", res)
	}
	if s := res.Steps[0]; !s.Ran || !s.OK || s.Round != 1 || res.Steps[1].Round != 2 || res.Reply != "The notes say ship on Friday." {
		t.Fatalf("steps %+v reply %q", res.Steps, res.Reply)
	}
	second := (*prompts)[1]
	if !strings.Contains(second, "execution_report") || !strings.Contains(second, "ship on Friday") {
		t.Fatal("the read's result did not reach the next round's prompt")
	}
	if !strings.Contains(second, "Read the named file once.") {
		t.Fatal("the lesson was not carried into the follow-up round")
	}
}

// A round with anything but read-only file steps ends the replay before any
// of its steps run: no command, no write, not even the reads beside them.
func TestReplayRoundsNeverRunsAnythingElse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o600)
	for name, plan := range map[string]string{
		"shell": `{"intent":"shell","steps":[{"tool":"file","action":"read","args":{"path":"notes.md"}},{"tool":"shell","command":"touch replay-marker.txt"}]}`,
		"write": `{"intent":"file","steps":[{"tool":"file","action":"write","args":{"path":"replay-marker.txt","content":"x"}}]}`,
		"edit":  `{"intent":"file","steps":[{"tool":"file","action":"edit","args":{"path":"notes.md","old_string":"x","new_string":"y"}}]}`,
		"later": `{"intent":"file","steps":[{"tool":"file","action":"read","args":{"path":"notes.md"}}]}`,
	} {
		answers := []string{plan}
		if name == "later" {
			// A read first, then a write in the follow-up round.
			answers = append(answers, `{"intent":"file","steps":[{"tool":"file","action":"write","args":{"path":"replay-marker.txt","content":"x"}}]}`)
		}
		stubPlannerSequence(t, answers...)
		ag, _ := newTestAgent(t)
		res, err := ag.ReplayRounds("do something", nil, 4)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.End != ReplayEndUnexecuted {
			t.Errorf("%s: end %q", name, res.End)
		}
		if _, err := os.Stat(filepath.Join(dir, "replay-marker.txt")); !os.IsNotExist(err) {
			t.Fatalf("%s: a replay executed a step it must never run", name)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "notes.md")); string(b) != "x" {
			t.Fatalf("%s: a replay changed a file", name)
		}
		for _, s := range res.Steps {
			if s.Ran && name != "later" {
				t.Errorf("%s: step %+v ran in a round that contained an unexecuted step", name, s.Step)
			}
		}
	}
}

// A planner that keeps reading runs out of the live follow-up budget for a
// file lookup: one first round plus three follow-ups.
func TestReplayRoundsStopAtTheLiveBudget(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("x"), 0o600)
	stubPlannerSequence(t, readNotes)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("what do my notes say?", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1+fileRetrievalBudget || res.End != ReplayEndBudget {
		t.Fatalf("rounds %d end %q", res.Rounds, res.End)
	}
	// The caller's cap applies too.
	res, _ = ag.ReplayRounds("what do my notes say?", nil, 2)
	if res.Rounds != 2 || res.End != ReplayEndBudget {
		t.Fatalf("capped: rounds %d end %q", res.Rounds, res.End)
	}
}

// One round is the v1 replay: nothing runs.
func TestReplayRoundsOneRoundExecutesNothing(t *testing.T) {
	t.Chdir(t.TempDir())
	stubPlannerSequence(t, readNotes)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("what do my notes say?", nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 || res.End != ReplayEndPlanned || res.Steps[0].Ran {
		t.Fatalf("result %+v", res)
	}
}

// A read that finds nothing ends a live non-agentic turn (there is nothing
// to answer from), and so it ends the replay.
func TestReplayRoundsStopWhenTheFirstReadFails(t *testing.T) {
	t.Chdir(t.TempDir())
	stubPlannerSequence(t, readNotes, answerNotes)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("what do my notes say?", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1 || res.End != ReplayEndAnswered || res.Steps[0].OK || res.Steps[0].Err == "" {
		t.Fatalf("result %+v", res)
	}
}

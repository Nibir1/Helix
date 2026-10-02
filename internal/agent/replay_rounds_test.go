package agent

import (
	"fmt"
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

// The bug multi-round replay surfaced: a read of a long document showed the
// planner an arbitrary slice from its middle (the tail of the first 20 KB),
// so it could never reach the section it was asked about and reread until
// the budget ran out. Now the first read shows the start and says where the
// file goes on, and a read with start_line shows the part asked for.
func TestReadsShowThePlannerTheLinesItIsTold(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var b strings.Builder
	for i := 1; i <= 963; i++ {
		switch i {
		case 840:
			b.WriteString("## 11. Replay: it plans past requests again and executes nothing.\n")
		case 2:
			b.WriteString("\n") // a blank line survives a read
		default:
			fmt.Fprintf(&b, "line %d of the harness doc\n", i)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "harness.md"), []byte(b.String()), 0o600)

	prompts := stubPlannerSequence(t,
		`{"intent":"file","steps":[{"tool":"file","action":"read","args":{"path":"harness.md"}}]}`,
		`{"intent":"file","steps":[{"tool":"file","action":"read","args":{"path":"harness.md","start_line":"835"}}]}`,
		`{"intent":"chat","steps":[{"tool":"response","message":"It plans past requests again."}]}`)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("explain how replay works, from harness.md", nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 3 || res.End != ReplayEndAnswered {
		t.Fatalf("rounds %d end %q", res.Rounds, res.End)
	}
	first, second := (*prompts)[1], (*prompts)[2]
	if !strings.Contains(first, "file contents") || !strings.Contains(first, "[harness.md: lines 1-80 of 963]") ||
		!strings.Contains(first, "line 1 of the harness doc\n  | \n  | line 3") || strings.Contains(first, "line 81 of") ||
		!strings.Contains(first, "start_line=81") {
		t.Fatalf("the first read's report:\n%s", first)
	}
	if !strings.Contains(second, "[harness.md: lines 835-914 of 963]") || !strings.Contains(second, "## 11. Replay") {
		t.Fatalf("the ranged read's report:\n%s", second)
	}
	if got := StepSubject(res.Steps[1].Step); got != "read harness.md (from line 835)" {
		t.Fatalf("subject %q", got)
	}
}

// Command output keeps its tail: errors print last.
func TestCommandOutputStillKeepsItsTail(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "out %d\n", i)
	}
	got := sanitizeOutput(b.String(), 5, 1000)
	if !strings.HasPrefix(got, "out 96") || !strings.HasSuffix(got, "out 100") {
		t.Fatalf("tail: %q", got)
	}
}

// readWindow is a plan that reads one window of doc.md.
func readWindow(start int) string {
	return fmt.Sprintf(`{"intent":"file","steps":[{"tool":"file","action":"read","args":{"path":"doc.md","start_line":"%d"}}]}`, start)
}

func longDoc(t *testing.T) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= 2000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile("doc.md", []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Reading new parts of a file earns rounds, up to the cap; the last round
// carries the notice, and only the last.
func TestProgressEarnsRoundsUpToTheCap(t *testing.T) {
	t.Chdir(t.TempDir())
	longDoc(t)
	var plans []string
	for i := 0; i < 10; i++ {
		plans = append(plans, readWindow(1+80*i))
	}
	prompts := stubPlannerSequence(t, plans...)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("what does doc.md say?", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 1+maxFileRetrievalBudget || res.End != ReplayEndBudget {
		t.Fatalf("rounds %d end %q, want %d budget-exhausted", res.Rounds, res.End, 1+maxFileRetrievalBudget)
	}
	for i, p := range *prompts {
		last := i == len(*prompts)-1
		if strings.Contains(p, "THIS IS THE LAST ROUND") != last {
			t.Errorf("prompt %d of %d: last-round notice present = %v", i+1, len(*prompts), !last)
		}
	}
}

// Repeating a step, or only searching, earns nothing: the turn keeps the
// base budget of 3 follow-ups.
func TestRepeatsAndSearchesEarnNoRounds(t *testing.T) {
	t.Chdir(t.TempDir())
	longDoc(t)
	cases := map[string][]string{
		"repeat": {readWindow(1), readWindow(81), readWindow(81), readWindow(1), readWindow(81), readWindow(1)},
		"search": {
			`{"intent":"file","steps":[{"tool":"file","action":"glob","args":{"pattern":"**/*.py"}}]}`,
			`{"intent":"file","steps":[{"tool":"file","action":"glob","args":{"pattern":"**/*config*"}}]}`,
			`{"intent":"file","steps":[{"tool":"file","action":"grep","args":{"pattern":"load_config"}}]}`,
			`{"intent":"file","steps":[{"tool":"file","action":"glob","args":{"pattern":"**/*.yaml"}}]}`,
			`{"intent":"file","steps":[{"tool":"file","action":"glob","args":{"pattern":"**/*.toml"}}]}`,
		},
	}
	for name, plans := range cases {
		prompts := stubPlannerSequence(t, plans...)
		ag, _ := newTestAgent(t)
		res, err := ag.ReplayRounds("find it", nil, 20)
		if err != nil {
			t.Fatal(err)
		}
		// "repeat": the first follow-up (81) is new and earns one round; every
		// round after it repeats a window and earns nothing.
		want := 1 + fileRetrievalBudget
		if name == "repeat" {
			want++
		}
		if res.Rounds != want || res.End != ReplayEndBudget {
			t.Errorf("%s: rounds %d end %q, want %d", name, res.Rounds, res.End, want)
		}
		if !strings.Contains((*prompts)[len(*prompts)-1], "THIS IS THE LAST ROUND") {
			t.Errorf("%s: the last round had no notice", name)
		}
	}
}

// A turn told it is on its last round answers from what it has. The
// replay records that as answered, not as running out.
func TestLastRoundAnswerEndsAnswered(t *testing.T) {
	t.Chdir(t.TempDir())
	longDoc(t)
	prompts := stubPlannerSequence(t, readWindow(1), readWindow(1), readWindow(1),
		`{"intent":"chat","steps":[{"tool":"response","message":"From what I read: lines 1-80."}]}`)
	ag, _ := newTestAgent(t)
	res, err := ag.ReplayRounds("what does doc.md say?", nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rounds != 4 || res.End != ReplayEndAnswered || res.Reply == "" {
		t.Fatalf("rounds %d end %q reply %q", res.Rounds, res.End, res.Reply)
	}
	if !strings.Contains((*prompts)[3], "THIS IS THE LAST ROUND") || strings.Contains((*prompts)[2], "THIS IS THE LAST ROUND") {
		t.Fatal("the notice must be on round 4 and only there")
	}
}

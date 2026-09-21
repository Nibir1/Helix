// internal/agent/todo_report_test.go
// Purpose: the two defects the first real run exposed.
//
// A user ran the harness against three tasks THEY had written. The agent worked
// on all three, created none of its own, and:
//
//  1. every iteration reported no outstanding work, because the loop counted
//     only tasks the agent had CREATED — so the "still working" label never
//     appeared and the budget warning was skipped;
//  2. the run ended by printing nothing. The budget ran out, the prompt came
//     back, and a task sat in_progress with the edit it described never made.
//     The only way to find out was to type /todo.
package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/session"
)

func reportAgent(t *testing.T) *Agent {
	t.Helper()
	l, err := session.NewTodoListAt(filepath.Join(t.TempDir(), "todo.json"))
	if err != nil {
		t.Fatalf("todo list: %v", err)
	}
	return &Agent{Todos: l}
}

// DEFECT 1. A task the user wrote, adopted by the agent, is this run's work.
// Marking it in_progress is the agent saying "I am on this".
func TestAdoptingAUserTaskCountsAsOutstandingWork(t *testing.T) {
	a := reportAgent(t)
	it, err := a.Todos.Add("bump the version in package.json to 1.2.0")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	baseline := a.maxTodoID()
	a.beginTodoTurn()

	if a.agentWorkOutstanding(baseline) {
		t.Fatal("an untouched user task is already counted — the user's list is a work queue")
	}

	// The agent picks it up.
	if _, err := a.Todos.AgentSetState(it.ID, session.TodoInProgress, ""); err != nil {
		t.Fatalf("AgentSetState: %v", err)
	}
	a.noteTodoTouched(it.ID)

	if !a.agentWorkOutstanding(baseline) {
		t.Fatal("a task the agent adopted and is working on does not count as outstanding — " +
			"the loop stops mid-job and the budget warning never fires")
	}
}

// The property that clause must not break: a task nobody touched this turn
// still does not drive anything.
func TestUntouchedUserTasksStillDoNotDriveTheLoop(t *testing.T) {
	a := reportAgent(t)
	if _, err := a.Todos.Add("renew the domain"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := a.Todos.Add("call the accountant"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	baseline := a.maxTodoID()
	a.beginTodoTurn()

	if a.agentWorkOutstanding(baseline) {
		t.Fatal("the user's untouched list is driving the agentic loop")
	}
}

// A new turn owns none of the previous turn's tasks.
func TestTouchedSetResetsEachTurn(t *testing.T) {
	a := reportAgent(t)
	it, _ := a.Todos.Add("yesterday's job")
	baseline := a.maxTodoID()

	a.beginTodoTurn()
	_, _ = a.Todos.AgentSetState(it.ID, session.TodoInProgress, "")
	a.noteTodoTouched(it.ID)
	if !a.agentWorkOutstanding(baseline) {
		t.Fatal("the adopted task does not count during its own turn")
	}

	a.beginTodoTurn() // next turn
	if a.agentWorkOutstanding(baseline) {
		t.Fatal("a task adopted LAST turn still drives this one — an unrelated question " +
			"would spend its whole budget finishing yesterday's job")
	}
}

// outstandingTasks has to name the tasks, not just count them: "some work is
// open" is not actionable, and the report prints them.
func TestOutstandingTasksAreNamed(t *testing.T) {
	a := reportAgent(t)
	it, _ := a.Todos.Add("bump the version")
	baseline := a.maxTodoID()
	a.beginTodoTurn()
	_, _ = a.Todos.AgentSetState(it.ID, session.TodoInProgress, "")
	a.noteTodoTouched(it.ID)

	open := a.outstandingTasks(baseline)
	if len(open) != 1 {
		t.Fatalf("got %d outstanding tasks, want 1", len(open))
	}
	if open[0].Text != "bump the version" {
		t.Errorf("the task is not named: %q", open[0].Text)
	}
	if open[0].ID != it.ID {
		t.Errorf("id = %d, want %d — the user cannot look it up", open[0].ID, it.ID)
	}
}

// Settled work is what a successful run has to be able to report. Without it a
// run that finished everything says nothing and looks the same as one that
// stalled.
func TestSettledThisTurnCountsOnlyWhatThisTurnClosed(t *testing.T) {
	a := reportAgent(t)
	old, _ := a.Todos.Add("closed last week")
	_, _ = a.Todos.AgentSetState(old.ID, session.TodoDone, "done earlier")

	mine, _ := a.Todos.Add("closed this turn")
	other, _ := a.Todos.Add("set aside this turn")
	a.beginTodoTurn()

	if n := a.settledThisTurn(); n != 0 {
		t.Errorf("settled = %d before this turn closed anything, want 0 — a turn that "+
			"did nothing would claim last week's work", n)
	}

	_, _ = a.Todos.AgentSetState(mine.ID, session.TodoDone, "finished")
	a.noteTodoTouched(mine.ID)
	_, _ = a.Todos.AgentSetState(other.ID, session.TodoSuperseded, "not needed after all")
	a.noteTodoTouched(other.ID)

	if n := a.settledThisTurn(); n != 2 {
		t.Errorf("settled = %d, want 2 (done and superseded both count as closed)", n)
	}
}

func TestPluralReadsLikeEnglish(t *testing.T) {
	if got := plural(1, "task"); got != "1 task" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(3, "task"); got != "3 tasks" {
		t.Errorf("plural(3) = %q", got)
	}
	if got := plural(0, "task"); got != "0 tasks" {
		t.Errorf("plural(0) = %q", got)
	}
}

// allLines is a renderer that keeps everything, so a test can assert the run
// said SOMETHING rather than which severity it chose.
type allLines struct{ lines []string }

func (r *allLines) PrintSystemMessage(m string)     { r.lines = append(r.lines, m) }
func (r *allLines) PrintAIMessage(m string, _ bool) { r.lines = append(r.lines, m) }
func (r *allLines) PrintCommand(m string)           { r.lines = append(r.lines, m) }
func (r *allLines) PrintData(m string)              { r.lines = append(r.lines, m) }
func (r *allLines) PrintSuccess(m string)           { r.lines = append(r.lines, m) }
func (r *allLines) PrintError(m string)             { r.lines = append(r.lines, m) }
func (r *allLines) PrintWarning(m string)           { r.lines = append(r.lines, m) }
func (r *allLines) PrintInfo(m string)              { r.lines = append(r.lines, m) }
func (r *allLines) PrintDebug(m string)             { r.lines = append(r.lines, m) }
func (r *allLines) PrintChrome(m string)            { r.lines = append(r.lines, m) }
func (r *allLines) Interactive() bool               { return false }

func (r *allLines) joined() string { return strings.Join(r.lines, "\n") }

// DEFECT 2, and the one the user asked for by name: however a run ends, it
// tells you. The first real run printed NOTHING — the budget ran out, the
// prompt came back, and a task sat in progress with its edit never made.
func TestEveryRunEndSaysSomething(t *testing.T) {
	okObs := []StepObservation{{Index: 0, Tool: "file", OK: true}}
	failObs := []StepObservation{{Index: 0, Tool: "shell", OK: false, Err: "exit 1"}}

	cases := []struct {
		name       string
		obs        []StepObservation
		leaveOpen  bool
		exhausted  bool
		want       []string
		mustNotSay string
		mustNotBe  string
	}{
		{
			name: "out of budget with work open", obs: okObs, leaveOpen: true, exhausted: true,
			want:      []string{"Step budget reached", "still open", "bump the version", "/todo"},
			mustNotBe: "the exact silence the first real run ended in",
		},
		{
			name: "stopped early with work open", obs: okObs, leaveOpen: true, exhausted: false,
			want: []string{"still open", "bump the version"},
			// And it must NOT claim the budget ran out — "it gave up" and "it
			// ran out of road" are different things to tell a user, and the
			// second sends them to /agentic steps for no reason.
			mustNotSay: "Step budget reached",
		},
		{
			name: "failed", obs: failObs, leaveOpen: false, exhausted: true,
			want: []string{"unresolved error"},
		},
		{
			name: "finished everything", obs: okObs, leaveOpen: false, exhausted: false,
			want: []string{"Done", "closed"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &allLines{}
			l, err := session.NewTodoListAt(filepath.Join(t.TempDir(), "todo.json"))
			if err != nil {
				t.Fatal(err)
			}
			a := &Agent{Todos: l, render: r}

			it, _ := l.Add("bump the version")
			baseline := a.maxTodoID()
			a.beginTodoTurn()

			end := session.TodoInProgress
			if !c.leaveOpen {
				end = session.TodoDone
			}
			if _, err := l.AgentSetState(it.ID, end, "worked on it"); err != nil {
				t.Fatal(err)
			}
			a.noteTodoTouched(it.ID)

			a.reportRunEnd(c.obs, baseline, 4, c.exhausted)

			out := r.joined()
			if strings.TrimSpace(out) == "" {
				t.Fatalf("the run ended in SILENCE — %s", c.mustNotBe)
			}
			for _, want := range c.want {
				if !strings.Contains(out, want) {
					t.Errorf("report never mentions %q:\n%s", want, out)
				}
			}
			if c.mustNotSay != "" && strings.Contains(out, c.mustNotSay) {
				t.Errorf("report wrongly says %q:\n%s", c.mustNotSay, out)
			}
			t.Logf("reported:\n%s", out)
		})
	}
}

// M3's gap: the tests above call noteTodoTouched directly, so deleting the call
// from the step handler broke nothing. This drives the real handler, which is
// the only path a live run takes.
func TestHandlingATodoStepRecordsTheTouch(t *testing.T) {
	l, err := session.NewTodoListAt(filepath.Join(t.TempDir(), "todo.json"))
	if err != nil {
		t.Fatal(err)
	}
	it, err := l.Add("bump the version in package.json")
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{Todos: l, render: &allLines{}, permission: PermissionAuto}
	baseline := a.maxTodoID()
	a.beginTodoTurn()

	// Exactly what the planner emitted in the real run that exposed this.
	step := ai.PlanStep{Tool: "todo", Action: "state", Args: map[string]string{
		"id": itoa(it.ID), "state": "in_progress",
	}}
	if _, err := a.handleTodoStep(step); err != nil {
		t.Fatalf("handleTodoStep: %v", err)
	}

	if !a.todoTouched[it.ID] {
		t.Fatal("handleTodoStep did not record the touch — a run working only on the " +
			"user's tasks reports no outstanding work and ends silently")
	}
	if !a.agentWorkOutstanding(baseline) {
		t.Fatal("adopting the task through the real handler still leaves the loop thinking " +
			"it has nothing to do")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The loop's own directive must repeat the stale-evidence rule. The planner
// prompt states it once at turn start; by round three the observation report
// and the directive are what the model is actually reading, and the first real
// run closed "run the tests" from a run that predated its own edit.
func TestDirectiveWarnsAgainstStaleVerification(t *testing.T) {
	a := reportAgent(t)
	it, _ := a.Todos.Add("run the tests")
	baseline := a.maxTodoID()
	a.beginTodoTurn()
	_, _ = a.Todos.AgentSetState(it.ID, session.TodoInProgress, "")
	a.noteTodoTouched(it.ID)

	d := a.todoDirective(baseline)
	if d == "" {
		t.Fatal("no directive with work outstanding")
	}
	for _, want := range []string{"verification task", "BEFORE your most recent change", "Re-run"} {
		if !strings.Contains(d, want) {
			t.Errorf("the directive never says %q — nothing stops a verification task "+
				"being closed on a result that predates the edit:\n%s", want, d)
		}
	}
}

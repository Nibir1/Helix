// internal/agent/todo_loop_test.go
// Purpose: the task list has to actually steer the agentic loop.
//
// The loop's stop condition used to be "the last batch of steps exited 0".
// Under that rule an agent that writes a five-step plan and finishes step one
// is stopped there, with four steps it declared necessary left undone and
// nothing saying so. "The last thing worked" is not "the work is finished", and
// only the list can tell them apart — so if agentWorkOutstanding is wrong, the
// task list is a display and nothing more.
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/session"
)

func loopAgent(t *testing.T) *Agent {
	t.Helper()
	l, err := session.NewTodoListAt(filepath.Join(t.TempDir(), "todo.json"))
	if err != nil {
		t.Fatalf("todo list: %v", err)
	}
	return &Agent{Todos: l}
}

func TestNoTaskListMeansNoOutstandingWork(t *testing.T) {
	a := &Agent{}
	if a.agentWorkOutstanding(0) {
		t.Error("an agent with no task list reported outstanding work; the loop would " +
			"run to its full budget on every turn")
	}
	if a.maxTodoID() != 0 {
		t.Error("maxTodoID is non-zero with no list")
	}
}

// An open task the agent added this turn keeps the loop going. This is the
// whole feature.
func TestOpenAgentTaskKeepsTheLoopRunning(t *testing.T) {
	a := loopAgent(t)
	baseline := a.maxTodoID()

	if a.agentWorkOutstanding(baseline) {
		t.Fatal("an empty list reported outstanding work")
	}
	if _, err := a.Todos.AgentAdd("run the tests", "the change is unverified"); err != nil {
		t.Fatalf("AgentAdd: %v", err)
	}
	if !a.agentWorkOutstanding(baseline) {
		t.Fatal("a task the agent just added does not keep the loop running — the " +
			"harness would stop with its own plan unfinished")
	}
}

// Finishing closes it, and so does superseding. Without this the loop runs to
// its budget every time: an agent that never marks anything done always has
// outstanding work.
func TestSettlingATaskStopsTheLoop(t *testing.T) {
	for _, end := range []session.TodoState{session.TodoDone, session.TodoSuperseded} {
		a := loopAgent(t)
		baseline := a.maxTodoID()
		it, err := a.Todos.AgentAdd("run the tests", "")
		if err != nil {
			t.Fatalf("AgentAdd: %v", err)
		}
		if _, err := a.Todos.AgentSetState(it.ID, end, "settled"); err != nil {
			t.Fatalf("AgentSetState(%s): %v", end, err)
		}
		if a.agentWorkOutstanding(baseline) {
			t.Errorf("a task marked %s still keeps the loop running", end)
		}
	}
}

// THE USER'S OWN LIST IS NOT A WORK QUEUE. "Renew the domain" sitting in /todo
// must never make an unrelated question spend planner calls, and treating it as
// work would turn every turn into an unrequested attempt at everything the user
// is tracking.
func TestUserTasksNeverDriveTheLoop(t *testing.T) {
	a := loopAgent(t)
	if _, err := a.Todos.Add("renew the domain"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := a.Todos.Add("call the accountant"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	baseline := a.maxTodoID()

	if a.agentWorkOutstanding(baseline) {
		t.Fatal("the user's own open tasks are driving the agentic loop")
	}
	// Even with the baseline taken before they existed.
	if a.agentWorkOutstanding(0) {
		t.Fatal("user tasks count as agent work regardless of baseline")
	}
}

// A task left open by an EARLIER run must not make today's unrelated question
// finish yesterday's job.
func TestStaleAgentTasksDoNotDriveALaterTurn(t *testing.T) {
	a := loopAgent(t)
	if _, err := a.Todos.AgentAdd("left over from this morning", ""); err != nil {
		t.Fatalf("AgentAdd: %v", err)
	}

	// A new turn takes its baseline now — after the stale task exists.
	baseline := a.maxTodoID()
	if a.agentWorkOutstanding(baseline) {
		t.Fatal("a task from a previous run is driving this turn's loop")
	}

	// And a task added during THIS turn still counts.
	if _, err := a.Todos.AgentAdd("this turn's work", ""); err != nil {
		t.Fatalf("AgentAdd: %v", err)
	}
	if !a.agentWorkOutstanding(baseline) {
		t.Fatal("this turn's own task does not count")
	}
}

// The directive is what tells the model to close what it finished. Without it
// the loop would run to its budget on every multi-step job.
func TestDirectiveAppearsOnlyWithOutstandingWorkAndSaysToCloseTasks(t *testing.T) {
	a := loopAgent(t)
	baseline := a.maxTodoID()

	if d := a.todoDirective(baseline); d != "" {
		t.Errorf("a directive was added with nothing outstanding: %q", d)
	}

	if _, err := a.Todos.AgentAdd("run the tests", ""); err != nil {
		t.Fatalf("AgentAdd: %v", err)
	}
	d := a.todoDirective(baseline)
	if d == "" {
		t.Fatal("no directive with work outstanding")
	}
	// Wording, not phrasing: the directive must tell the model to CLOSE what it
	// finished and to supersede what turned out unnecessary. Asserting an exact
	// sentence makes every rewrite a test failure for no gain.
	for _, want := range []string{"mark it done", "supersede"} {
		if !strings.Contains(d, want) {
			t.Errorf("the directive never mentions %q, so a model that finishes its "+
				"work but leaves it open will loop to the budget:\n%s", want, d)
		}
	}
}

// THE WIRING, not the predicate. agentWorkOutstanding being correct is worth
// nothing if the loop does not consult it — and reverting the loop to its old
// two-clause condition left every other test in this file passing. This is the
// test that noticed.
func TestTheLoopStopsOnlyWhenTheAgentsOwnPlanIsDone(t *testing.T) {
	ok := []StepObservation{{Index: 0, Tool: "shell", OK: true}}
	failed := []StepObservation{{Index: 0, Tool: "shell", OK: false, Err: "boom"}}
	retrieved := []StepObservation{{Index: 0, Tool: "web", OK: true, NeedsAnswer: true}}

	cases := []struct {
		name      string
		obs       []StepObservation
		moreWork  bool
		wantStop  bool
		breakName string
	}{
		{"everything succeeded and nothing is outstanding", ok, false, true,
			"the loop would keep planning with nothing left to do"},
		{"everything succeeded but the agent's plan has work left", ok, true, false,
			"the loop stops with the agent's own plan unfinished — the task list becomes a display"},
		{"a step failed", failed, false, false,
			"the self-correction loop would not correct anything"},
		{"a step failed and work is outstanding", failed, true, false, "both reasons to continue were ignored"},
		{"a retrieval needs answering", retrieved, false, false,
			"a search would run and never be turned into a reply"},
		{"a retrieval needs answering and work is outstanding", retrieved, true, false, "both reasons ignored"},
	}
	for _, c := range cases {
		if got := followUpDone(c.obs, c.moreWork); got != c.wantStop {
			t.Errorf("%s: followUpDone = %v, want %v — %s", c.name, got, c.wantStop, c.breakName)
		}
	}
}

// And the loop must actually CALL it. followUpDone can be correct and unused:
// inlining the old two-clause condition at the call site still compiles, still
// leaves `moreWork` used by the label below it, and quietly restores the bug.
// This is the residual hole the behavioural test above cannot see.
func TestTheLoopUsesTheExtractedStopDecision(t *testing.T) {
	src, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatalf("read harness.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, "if followUpDone(obs, moreWork) {") {
		t.Error("agenticFollowUp does not stop via followUpDone — the stop decision " +
			"has been inlined, which is how the task list silently became a display")
	}
	// The old condition must not reappear as a bare stop anywhere in the loop.
	if strings.Contains(body, "if allStepsOK(obs) && !needsAnswer(obs) {") {
		t.Error("the two-clause stop condition is back in harness.go; it ignores the " +
			"agent's own outstanding work")
	}
}

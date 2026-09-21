// internal/session/todo_agent_test.go
// Purpose: the agent may redirect anything on the list and erase only its own.
//
// Every property here is one that would not fail loudly if it broke. A user
// task the agent silently deleted is a task the user still believes is tracked;
// a revision that overwrote the original wording leaves nothing to compare
// against. Nothing crashes, nothing logs — the plan is just quietly not the one
// that was agreed.
package session

import (
	"strings"
	"testing"
)

// agentList reuses the package's existing newList helper, dropping the path it
// returns — none of these tests care where the file lives.
func agentList(t *testing.T) *TodoList {
	t.Helper()
	l, _ := newList(t)
	return l
}

func mustUserTask(t *testing.T, l *TodoList, text string) TodoItem {
	t.Helper()
	it, err := l.Add(text)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	return it
}

// --------------------------------------------------------------- provenance

// The zero value must be "user". Every item in a todo.json written before the
// agent could write anything was typed by a human, so an absent origin
// decoding to "user" is the correct answer, not a fallback.
func TestExistingTasksAreAttributedToTheUser(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "renew the domain")
	if it.Origin.ByAgent() {
		t.Fatal("a task added through the user API is attributed to the agent")
	}
	if it.Origin.Label() != "you" {
		t.Errorf("origin label = %q, want %q", it.Origin.Label(), "you")
	}
}

func TestAgentTasksAreAttributedToTheAgent(t *testing.T) {
	l := agentList(t)
	it, err := l.AgentAdd("add the retry guard", "the transport has no backoff")
	if err != nil {
		t.Fatalf("AgentAdd: %v", err)
	}
	if !it.Origin.ByAgent() {
		t.Fatal("a task the agent added is not attributed to it")
	}
	if it.Reason == "" {
		t.Error("the agent's reason was not kept")
	}
}

// ------------------------------------------------- the one thing it cannot do

// Deletion is the only operation that cannot be seen or undone, which is why it
// is the only one reserved. The agent's judgment is not the issue — the record
// is.
func TestAgentCannotDeleteAUserTask(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "ship the release")

	err := l.AgentRemove(it.ID)
	if err == nil {
		t.Fatal("the agent deleted a task the user wrote")
	}
	if !strings.Contains(err.Error(), "superseded") {
		t.Errorf("the refusal does not offer the way forward: %v", err)
	}
	if len(l.Items()) != 1 {
		t.Fatal("the task is gone despite the refusal")
	}
}

func TestAgentCanDeleteItsOwnTask(t *testing.T) {
	l := agentList(t)
	it, err := l.AgentAdd("scratch note", "")
	if err != nil {
		t.Fatalf("AgentAdd: %v", err)
	}
	if err := l.AgentRemove(it.ID); err != nil {
		t.Fatalf("the agent could not delete its own task: %v", err)
	}
	if len(l.Items()) != 0 {
		t.Fatal("the agent's own task survived deletion")
	}
}

// ------------------------------------------------ what it CAN do, and the cost

// Superseding is the agent's real authority over a user's plan: the task stops
// being outstanding work, and the record of it survives.
func TestAgentCanSupersedeAUserTaskWithAReason(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "add a retry loop to transport.go")

	got, err := l.AgentSetState(it.ID, TodoSuperseded, "transport.go:88 already retries with backoff")
	if err != nil {
		t.Fatalf("AgentSetState: %v", err)
	}
	if got.State != TodoSuperseded {
		t.Errorf("state = %q, want superseded", got.State)
	}
	if !strings.Contains(got.Reason, "transport.go:88") {
		t.Errorf("the reason was not kept: %q", got.Reason)
	}
	// Gone from the plan, still on the list.
	if s := l.Summary(0); strings.Contains(s, "retry loop") {
		t.Errorf("a superseded task is still presented to the planner as open work:\n%s", s)
	}
	if len(l.Items()) != 1 {
		t.Error("superseding destroyed the record")
	}
}

// An overrule with no stated reason is indistinguishable from a mistake, so the
// two states that overrule the user require one.
func TestOverrulingAUserTaskRequiresAReason(t *testing.T) {
	for _, state := range []TodoState{TodoDone, TodoSuperseded} {
		l := agentList(t)
		it := mustUserTask(t, l, "write the migration")
		if _, err := l.AgentSetState(it.ID, state, ""); err == nil {
			t.Errorf("the agent marked a user task %s with no reason", state)
		}
	}
}

// Progress is not an overrule — marking a task in progress says nothing the
// user would dispute, and demanding a reason for it would train the model to
// emit filler.
func TestProgressOnAUserTaskNeedsNoReason(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "write the migration")
	if _, err := l.AgentSetState(it.ID, TodoInProgress, ""); err != nil {
		t.Fatalf("marking a user task in progress required a reason: %v", err)
	}
}

// The agent's own tasks are its own business.
func TestAgentNeedsNoReasonForItsOwnTasks(t *testing.T) {
	l := agentList(t)
	it, _ := l.AgentAdd("read the transport", "")
	if _, err := l.AgentSetState(it.ID, TodoDone, ""); err != nil {
		t.Fatalf("the agent had to justify finishing its own task: %v", err)
	}
}

// ----------------------------------------------------------------- revision

// The agent may change the plan. It may not change the record of what was
// asked for.
func TestRevisionPreservesWhatTheUserWrote(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "bump the version in package.json")

	got, err := l.AgentRevise(it.ID, "bump the version in config.go — there is no package.json",
		"this is a Go repo; the version lives in internal/config")
	if err != nil {
		t.Fatalf("AgentRevise: %v", err)
	}
	if !got.Revised() {
		t.Fatal("a revised user task does not report that it was revised")
	}
	if got.WasText != "bump the version in package.json" {
		t.Errorf("the user's original wording is %q, want it preserved", got.WasText)
	}
	if !strings.Contains(got.Text, "config.go") {
		t.Errorf("the revision did not take: %q", got.Text)
	}
}

// A SECOND revision must not overwrite the user's wording with the agent's own
// previous attempt — that would launder agent text into the provenance field
// and lose the one thing it exists to keep.
func TestASecondRevisionKeepsTheUsersOriginal(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "the original request")

	if _, err := l.AgentRevise(it.ID, "first attempt", "because"); err != nil {
		t.Fatalf("first revise: %v", err)
	}
	got, err := l.AgentRevise(it.ID, "second attempt", "on reflection")
	if err != nil {
		t.Fatalf("second revise: %v", err)
	}
	if got.WasText != "the original request" {
		t.Errorf("WasText = %q after two revisions; the agent's own first attempt "+
			"has overwritten what the user actually wrote", got.WasText)
	}
}

func TestRevisingAUserTaskRequiresAReason(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "do the thing")
	if _, err := l.AgentRevise(it.ID, "do the other thing", ""); err == nil {
		t.Fatal("the agent rewrote a user task with no reason given")
	}
}

// Emptying the text is deletion wearing a different hat.
func TestRevisionCannotEmptyATask(t *testing.T) {
	l := agentList(t)
	it := mustUserTask(t, l, "do the thing")
	if _, err := l.AgentRevise(it.ID, "   ", "because"); err == nil {
		t.Fatal("a task was blanked by revision, which is deletion by another route")
	}
}

// ------------------------------------------------------------------- bounds

func TestAgentListIsBounded(t *testing.T) {
	l := agentList(t)
	for i := 0; i < maxAgentTodos; i++ {
		if _, err := l.AgentAdd("task", ""); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	_, err := l.AgentAdd("one too many", "")
	if err == nil {
		t.Fatal("the agent filled the list without limit; it is injected into every prompt")
	}
	if !strings.Contains(err.Error(), "supersede") {
		t.Errorf("the limit does not say how to get under it: %v", err)
	}
}

// Settled tasks do not count against the cap — otherwise a long job wedges
// itself after thirty completed steps.
func TestSettledTasksDoNotCountAgainstTheCap(t *testing.T) {
	l := agentList(t)
	for i := 0; i < maxAgentTodos; i++ {
		it, err := l.AgentAdd("task", "")
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		if _, err := l.AgentSetState(it.ID, TodoDone, ""); err != nil {
			t.Fatalf("done %d: %v", i, err)
		}
	}
	if _, err := l.AgentAdd("the next real task", ""); err != nil {
		t.Fatalf("a list of finished tasks blocked new work: %v", err)
	}
}

// --------------------------------------------------------- the planner's view

// The planner cannot act on an item it cannot name, and cannot tell an overrule
// from bookkeeping without knowing whose task it is.
func TestSummaryCarriesIDsAndAuthorship(t *testing.T) {
	l := agentList(t)
	mine := mustUserTask(t, l, "the user's task")
	theirs, _ := l.AgentAdd("the agent's task", "needed for the build")

	s := l.Summary(0)
	if !strings.Contains(s, "#"+itoa(mine.ID)) || !strings.Contains(s, "#"+itoa(theirs.ID)) {
		t.Errorf("the summary has no addressable ids:\n%s", s)
	}
	if !strings.Contains(s, "(by you)") {
		t.Errorf("the user's task is not marked as theirs:\n%s", s)
	}
	if !strings.Contains(s, "(by helix)") {
		t.Errorf("the agent's task is not marked as its own:\n%s", s)
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

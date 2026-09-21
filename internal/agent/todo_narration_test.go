// internal/agent/todo_narration_test.go
// Purpose: what Helix says out loud while it works, and the directive bug that
// made it say nothing useful at all.
package agent

import (
	"strings"
	"testing"

	"helix/internal/session"
)

// THE BUG THE SCREENSHOT SHOWED. "Read me the parser file and tell me what it
// does" produced glob, glob, glob, glob — and never a read, never an answer.
//
// The cause was the answer-only directive, written for the web tool and fired
// for any NeedsAnswer step. A glob returns PATHS; being told "your ONLY job now
// is to answer from those results" leaves the model with nothing true to say,
// because it has not read anything.
func TestFileDiscoveryIsNotToldToAnswerInstead(t *testing.T) {
	glob := []StepObservation{{Index: 0, Tool: "file", Action: "glob", OK: true, NeedsAnswer: true}}
	d := observationDirective(glob)

	if strings.Contains(d, "ONLY job now is to answer") {
		t.Fatal("a file discovery step is told its only job is to answer — from a list " +
			"of filenames. This is what made it glob forever and never read.")
	}
	if !strings.Contains(d, "READ what you found") {
		t.Errorf("the directive never tells it to read what the glob found:\n%s", d)
	}
	if !strings.Contains(strings.ToLower(d), "never describe a file you have not read") {
		t.Errorf("nothing stops it describing a file it only globbed for:\n%s", d)
	}
}

// And the web loop it was written for must still be stopped.
func TestWebRetrievalIsStillToldToAnswer(t *testing.T) {
	web := []StepObservation{{Index: 0, Tool: "web", Action: "search", OK: true, NeedsAnswer: true}}
	d := observationDirective(web)

	if !strings.Contains(d, "ONLY job now is to answer") {
		t.Fatal("the web answer-only directive is gone; the search loop it prevents comes back")
	}
	if !strings.Contains(d, "Do NOT emit another web step") {
		t.Error("the directive no longer forbids a repeat search")
	}
}

// A mixed plan must not get the web's answer-only instruction, or the file half
// is stranded exactly as before.
func TestMixedRetrievalDoesNotGetTheWebDirective(t *testing.T) {
	mixed := []StepObservation{
		{Index: 0, Tool: "web", Action: "search", OK: true, NeedsAnswer: true},
		{Index: 1, Tool: "file", Action: "glob", OK: true, NeedsAnswer: true},
	}
	if webRetrievalPending(mixed) {
		t.Error("a plan that also globbed is treated as a pure web retrieval")
	}
	if !webRetrievalPending([]StepObservation{
		{Tool: "web", OK: true, NeedsAnswer: true},
		{Tool: "shell", OK: true},
	}) {
		t.Error("a web search beside an ordinary command is no longer a web retrieval")
	}
}

// A file question is find → read → answer. With the web's single follow-up it
// spent its one iteration on the glob and stopped.
func TestFileQuestionsGetRoomToFindThenRead(t *testing.T) {
	fileObs := []StepObservation{{Tool: "file", Action: "glob", OK: true, NeedsAnswer: true}}
	webObs := []StepObservation{{Tool: "web", Action: "search", OK: true, NeedsAnswer: true}}

	if got := retrievalBudget(fileObs); got < 2 {
		t.Errorf("a file retrieval gets %d follow-up(s) — not enough to read what it "+
			"found, so it answers from filenames or not at all", got)
	}
	if got := retrievalBudget(webObs); got != retrievalFollowUpBudget {
		t.Errorf("a web retrieval's budget changed to %d; one hop is all it needs", got)
	}
}

// ------------------------------------------------------------------ narration

// The plan is announced once, naming the first task and counting the rest.
// Reading five tasks aloud is a list nobody retains.
func TestPlanIsAnnouncedWithTheFirstTaskAndACount(t *testing.T) {
	three := []session.TodoItem{
		{ID: 1, Text: "read parser.go", State: session.TodoPending},
		{ID: 2, Text: "bump the version", State: session.TodoPending},
		{ID: 3, Text: "run the tests", State: session.TodoPending},
	}
	got := planNarration(three)
	if !strings.Contains(got, "3 steps") {
		t.Errorf("the plan does not say how many steps: %q", got)
	}
	if !strings.Contains(got, "read parser.go") {
		t.Errorf("the plan does not name the first task: %q", got)
	}
	for _, later := range []string{"bump the version", "run the tests"} {
		if strings.Contains(got, later) {
			t.Errorf("the plan reads out every task; %q should be left to the screen: %q", later, got)
		}
	}
	if one := planNarration(three[:1]); strings.Contains(one, "steps") {
		t.Errorf("a single task is announced as steps: %q", one)
	}
}

// Each transition says what is happening now, and what comes next — the pair a
// listener needs to know whether to wait.
func TestProgressNarrationNamesNowAndNext(t *testing.T) {
	rest := []session.TodoItem{{ID: 2, Text: "bump the version", State: session.TodoPending}}

	start := todoNarration("state", session.TodoItem{ID: 1, Text: "read parser.go",
		State: session.TodoInProgress}, nil)
	if !strings.Contains(start, "read parser.go") {
		t.Errorf("starting a task does not name it: %q", start)
	}

	done := todoNarration("state", session.TodoItem{ID: 1, Text: "read parser.go",
		State: session.TodoDone}, rest)
	if !strings.Contains(done, "Done") || !strings.Contains(done, "bump the version") {
		t.Errorf("finishing a task does not say what comes next: %q", done)
	}

	last := todoNarration("state", session.TodoItem{ID: 1, Text: "read parser.go",
		State: session.TodoDone}, nil)
	if strings.Contains(last, "Next") {
		t.Errorf("the final task promises a next one: %q", last)
	}
}

// Adds are silent. A three-task plan arrives as three add steps, and narrating
// each is three interruptions for one decision.
func TestAddsAreSilent(t *testing.T) {
	if got := todoNarration("add", session.TodoItem{ID: 1, Text: "a task"}, nil); got != "" {
		t.Errorf("adding a task interrupts the conversation: %q", got)
	}
}

// Rewriting the USER's task is the one edit they would dispute, so it is always
// spoken.
func TestRewritingAUserTaskIsAlwaysSpoken(t *testing.T) {
	got := todoNarration("revise", session.TodoItem{
		ID: 2, Text: "bump the version in version.go", WasText: "bump the version in package.json",
	}, nil)
	if got == "" {
		t.Fatal("a rewrite of the user's own task happens silently")
	}
	if !strings.Contains(got, "your tasks") {
		t.Errorf("the line does not say it was the user's task: %q", got)
	}
}

// Nothing spoken carries detail the summary guard would withhold anyway.
func TestNarrationSpeaksNoPathsOrVersions(t *testing.T) {
	item := session.TodoItem{
		ID: 1, State: session.TodoInProgress,
		Text:   "bump /Users/me/proj/internal/config/config.go to v1.2.0 — see line 943",
		Reason: "the release script checks it",
	}
	got := todoNarration("state", item, nil)
	for _, banned := range []string{"/Users", "line 943", "the release script"} {
		if strings.Contains(got, banned) {
			t.Errorf("the spoken line carries %q, which belongs on screen: %q", banned, got)
		}
	}
}

// The closing line reports BOTH halves. "Three done" while two remain open
// sends someone away believing a job finished.
func TestClosingLineReportsWhatIsLeft(t *testing.T) {
	open := []session.TodoItem{{ID: 3, Text: "run the tests", State: session.TodoPending}}

	both := runEndNarration(2, open)
	if !strings.Contains(both, "2 done") || !strings.Contains(both, "still open") {
		t.Errorf("a partial run does not report both halves: %q", both)
	}
	if all := runEndNarration(3, nil); !strings.Contains(all, "All 3 done") {
		t.Errorf("a finished run does not say so: %q", all)
	}
	if one := runEndNarration(1, nil); strings.Contains(one, "All") {
		t.Errorf("one task is announced as though it were several: %q", one)
	}
	if none := runEndNarration(0, nil); none != "" {
		t.Errorf("a run that touched nothing still speaks: %q", none)
	}
}

// A task written for the screen carries detail that pushes the verb out of
// earshot when spoken.
func TestSpokenTaskTrimsScreenDetail(t *testing.T) {
	if got := spokenTask(`add validation to parser.go — it panics on ""`); strings.Contains(got, "panics") {
		t.Errorf("the em-dash detail survives into speech: %q", got)
	}
	long := strings.Repeat("refactor the transport layer ", 8)
	if got := spokenTask(long); len([]rune(got)) > 75 {
		t.Errorf("a long task is spoken at %d runes: %q", len([]rune(got)), got)
	}
	if got := spokenTask("run the tests"); got != "run the tests" {
		t.Errorf("a short task was altered: %q", got)
	}
}

// Silence when nobody is listening: narration must not print or block on a
// typed session.
func TestNarrationIsSilentWithoutVoice(t *testing.T) {
	a := &Agent{} // no OnSpeak, voice inactive
	a.speakPlan([]session.TodoItem{{ID: 1, Text: "a task", State: session.TodoPending}})
	a.speakTodoStep("state", session.TodoItem{ID: 1, State: session.TodoInProgress}, nil)
	a.speakRunEnd(1, nil)
	// Reaching here without a panic is the assertion: a nil OnSpeak and an
	// inactive voice must both be handled by the guard, not by luck.
}

// A SEARCH THAT FOUND NOTHING IS AN ANSWER. A real session asked for a file
// that did not exist and the model globbed eight times for it — the same
// pattern twice, then looser ones, then `**/*.py` in a Go repository — before
// concluding what the first result already said.
func TestEmptySearchResultStopsTheHunt(t *testing.T) {
	empty := []StepObservation{{
		Index: 0, Tool: "file", Action: "glob", OK: true, NeedsAnswer: true,
		Output: "(no files matched)",
	}}
	d := observationDirective(empty)

	if !strings.Contains(d, "NO MATCHES") {
		t.Fatalf("an empty search result is not called out at all:\n%s", d)
	}
	for _, want := range []string{"not a failure", "Do NOT retry", "looser pattern"} {
		if !strings.Contains(d, want) {
			t.Errorf("the directive never says %q, so the model keeps hunting:\n%s", want, d)
		}
	}

	// A search that DID find something must not get the stop instruction — that
	// would tell it to give up on a result it should be reading.
	found := []StepObservation{{
		Index: 0, Tool: "file", Action: "glob", OK: true, NeedsAnswer: true,
		Output: "internal/ai/planner.go",
	}}
	if strings.Contains(observationDirective(found), "NO MATCHES") {
		t.Error("a successful search is told it found nothing")
	}
}

// The wording is a closed set this repository owns; matching loosely would fire
// on a file whose CONTENTS happen to contain the phrase.
func TestEmptyResultMatchesOnlyTheToolsOwnWording(t *testing.T) {
	for _, out := range []string{"(no files matched)", "(no matches)", "(empty directory)"} {
		if !emptyResultSeen([]StepObservation{{Tool: "file", OK: true, Output: out}}) {
			t.Errorf("%q is not recognised as an empty result", out)
		}
	}
	// A file whose contents mention it is not an empty result.
	if emptyResultSeen([]StepObservation{{
		Tool: "file", OK: true,
		Output: "README.md:12: when the glob prints (no files matched) you should stop",
	}}) {
		t.Error("a file's CONTENTS were read as an empty search result")
	}
	// Neither is a web step.
	if emptyResultSeen([]StepObservation{{Tool: "web", OK: true, Output: "(no matches)"}}) {
		t.Error("a web result was treated as a file search")
	}
}

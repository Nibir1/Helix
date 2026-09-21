// cmd/helix/todo_view_test.go
// Purpose: the task list is rendered, so it is verified by RENDERING it (§9
// rule 12) rather than by reading the code that builds it.
//
// Three of the defects fixed here were invisible in the source and obvious the
// moment the output was printed: labels aligned to different columns because
// the pad counted BYTES and "↳" is three of them for one cell; continuation
// lines repeated the "↳" so a wrapped sentence read as a list of separate
// notes; and the settled count was rendered a third time on its own line beside
// a bar and a set of counts that already said it.
package main

import (
	"strings"
	"testing"

	"helix/internal/session"
	"helix/internal/shell"
)

func sample() []session.TodoItem {
	return []session.TodoItem{
		{ID: 1, Text: "add empty-input validation to parser.go", State: session.TodoDone,
			Reason: "parser.go already returns ErrEmpty"},
		{ID: 2, Text: "bump the version in version.go to 1.2.0", State: session.TodoInProgress,
			WasText: "bump the version in package.json to 1.2.0",
			Reason:  "there is no package.json here; this is a Go module and the version lives in version.go"},
		{ID: 3, Text: "run the tests", State: session.TodoPending},
		{ID: 4, Text: "update the CHANGELOG", State: session.TodoPending, Origin: session.TodoFromAgent},
	}
}

// Open work is read to decide what to do next. Burying the live item under
// eleven completed ones answers a question nobody asked.
func TestOpenWorkIsListedBeforeSettledWork(t *testing.T) {
	lines := todoView(sample(), 96)

	posOf := func(needle string) int {
		for i, l := range lines {
			if strings.Contains(l, needle) {
				return i
			}
		}
		return -1
	}
	inProgress := posOf("bump the version in version.go")
	done := posOf("add empty-input validation")
	if inProgress < 0 || done < 0 {
		t.Fatalf("a task is missing from the render:\n%s", strings.Join(lines, "\n"))
	}
	if inProgress > done {
		t.Errorf("settled work is listed above open work:\n%s", strings.Join(lines, "\n"))
	}
}

// THE ALIGNMENT BUG. "↳" is three bytes and one column, so a len()-based pad
// puts "you wrote" and "why" at different left edges and the second column
// stops being a column.
func TestNoteLabelsShareOneColumn(t *testing.T) {
	lines := todoView(sample(), 96)

	// The value of each note is known, so the column it starts at can be
	// measured directly rather than inferred from the label.
	want := map[string]string{
		"you wrote": "bump the version in package.json",
		"why":       "there is no package.json here",
	}
	starts := map[string]int{}
	for _, l := range lines {
		for label, value := range want {
			if !strings.Contains(l, "↳ "+label) || !strings.Contains(l, value) {
				continue
			}
			starts[label] = shell.Columns(l[:strings.Index(l, value)])
		}
	}
	if len(starts) != len(want) {
		t.Fatalf("only found %d of %d notes; the test proves nothing:\n%s",
			len(starts), len(want), strings.Join(lines, "\n"))
	}
	var first, firstLabel = -1, ""
	for label, at := range starts {
		if first < 0 {
			first, firstLabel = at, label
			continue
		}
		if at != first {
			t.Errorf("%q starts its value at column %d and %q at column %d — the labels "+
				"do not form a column:\n%s", label, at, firstLabel, first, strings.Join(lines, "\n"))
		}
	}
}

// A wrapped note is ONE note. Repeating the marker down its continuations reads
// as several.
func TestWrappedNotesDoNotRepeatTheMarker(t *testing.T) {
	items := []session.TodoItem{{
		ID: 1, Text: "a task", State: session.TodoPending,
		Reason: strings.Repeat("a reason long enough to wrap more than once in a narrow panel ", 4),
	}}
	lines := todoView(items, 60)

	markers := 0
	for _, l := range lines {
		if strings.Contains(l, "↳") {
			markers++
		}
	}
	if markers != 1 {
		t.Errorf("a single wrapped note rendered %d markers; it reads as %d separate "+
			"notes:\n%s", markers, markers, strings.Join(lines, "\n"))
	}
	if len(lines) < 3 {
		t.Fatalf("the note did not wrap, so nothing was tested:\n%s", strings.Join(lines, "\n"))
	}
}

// Every state gets its own marker. One shared bullet for pending, in progress
// and blocked made the live item — the only one worth looking for —
// indistinguishable from the queue behind it.
func TestEveryStateHasItsOwnGlyph(t *testing.T) {
	seen := map[string]session.TodoState{}
	for _, s := range []session.TodoState{
		session.TodoPending, session.TodoInProgress, session.TodoDone,
		session.TodoBlocked, session.TodoSuperseded,
	} {
		g := todoGlyph(s)
		if prev, dup := seen[g]; dup {
			t.Errorf("%q and %q share the glyph %q", prev, s, g)
		}
		seen[g] = s
	}
}

// A count of nothing is not information. A fresh three-task list used to read
// "3 pending · 0 in progress · 0 blocked · 0 done · 0 superseded".
func TestMeterOmitsEmptyStates(t *testing.T) {
	fresh := []session.TodoItem{
		{ID: 1, Text: "a", State: session.TodoPending},
		{ID: 2, Text: "b", State: session.TodoPending},
	}
	m := todoMeter(fresh, 96)
	// Only the COUNTS half; "0 of 2 settled" is a legitimate zero and lives in
	// the summary that follows them.
	counts, _, _ := strings.Cut(m, "   ")
	for _, empty := range []string{"0 in progress", "0 blocked", "0 done", "0 superseded"} {
		if strings.Contains(counts, empty) {
			t.Errorf("the meter reports an empty state (%q): %q", empty, m)
		}
	}
	if !strings.Contains(m, "2 pending") {
		t.Errorf("the meter does not report the state that IS present: %q", m)
	}
	if !strings.Contains(m, "0 of 2 settled") {
		t.Errorf("the meter does not say how much is settled: %q", m)
	}
}

func TestMeterBarTracksProgress(t *testing.T) {
	none := todoMeter([]session.TodoItem{
		{ID: 1, State: session.TodoPending}, {ID: 2, State: session.TodoPending},
	}, 96)
	all := todoMeter([]session.TodoItem{
		{ID: 1, State: session.TodoDone}, {ID: 2, State: session.TodoSuperseded},
	}, 96)

	if strings.Count(none, "▓") != 0 {
		t.Errorf("an untouched list shows a filled bar: %q", none)
	}
	if strings.Count(all, "░") != 0 {
		t.Errorf("a finished list shows an empty bar: %q", all)
	}
	// Superseded counts as settled: the agent decided it should not happen,
	// which is as finished as having done it.
	if !strings.Contains(all, "2 of 2 settled") {
		t.Errorf("superseded does not count as settled: %q", all)
	}
}

// The author is chrome and belongs at the edge; the task text is the headline
// and must never be truncated to make room for it.
func TestAuthorTagNeverEatsTheTaskText(t *testing.T) {
	long := strings.Repeat("a very long task description ", 5)
	items := []session.TodoItem{{ID: 1, Text: long, State: session.TodoPending, Origin: session.TodoFromAgent}}
	lines := todoView(items, 40)

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, strings.TrimSpace(long)) {
		t.Errorf("the task text was truncated to fit the author tag:\n%s", joined)
	}
	if !strings.Contains(joined, "helix") {
		t.Errorf("the author tag was dropped entirely:\n%s", joined)
	}
}

func TestEmptyListRendersNothing(t *testing.T) {
	if got := todoView(nil, 96); got != nil {
		t.Errorf("an empty list rendered %d lines", len(got))
	}
	if got := todoMeter(nil, 96); got != "" {
		t.Errorf("an empty list rendered a meter: %q", got)
	}
}

// Colour is applied after layout, so it must not change what is on the line.
// A paint pass that shifts a column silently undoes the alignment the layout
// worked for.
func TestPaintingDoesNotChangeTheLayout(t *testing.T) {
	for _, line := range todoView(sample(), 96) {
		if line == "" {
			continue
		}
		painted := shell.Plain(paintTodoLine(line))
		if painted != line {
			t.Errorf("painting changed the line:\n plain: %q\n after: %q", line, painted)
		}
	}
}

// The in-progress marker must not share a colour with settled work: it is the
// row the eye is looking for.
func TestInProgressIsTheOnlyHighlightedRow(t *testing.T) {
	inProgress, _ := todoRowColours("▸")
	done, _ := todoRowColours("✔")
	superseded, _ := todoRowColours("⊘")
	pending, _ := todoRowColours("·")

	if inProgress == done || inProgress == superseded || inProgress == pending {
		t.Errorf("the in-progress marker (%s) shares a colour with another state — "+
			"the live task does not stand out", inProgress)
	}
	if superseded != pending && superseded == done {
		t.Error("superseded and done are painted the same; one was decided against")
	}
}

// cmd/helix/todo_view.go
// Purpose: rendering the task list, as an instrument readout rather than a
// numbered list.
//
// WHAT THIS REPLACES. The list was printed with bare fmt.Printf: items floated
// outside the panel's gutter while the totals line sat behind it, so the block
// had two different left edges; the state marker was one chrome-coloured glyph
// for everything that was not done; the agent's notes were flat muted prose
// with no column to scan down; and the totals showed every state including the
// zeros, so a fresh list read "3 pending · 0 in progress · 0 blocked · 0 done ·
// 0 superseded" — five facts where one was true.
//
// The list also has two authors now, and none of the old rendering was built
// for that. Whose task it is, what you originally wrote, and why Helix changed
// it are the questions the screen has to answer at a glance.
//
// Split out of harness_cmds.go and written as functions returning LINES so the
// layout can be rendered in a test and read — §9 rule 12. The previous version
// could only be checked by running the shell and looking.
package main

import (
	"fmt"
	"strings"

	"helix/internal/session"
	"helix/internal/shell"
)

// todoView renders the whole panel body. Returns the lines between the title
// rule and the closing rule.
//
// Open work comes FIRST and settled work below it, which is the one ordering
// decision that matters here: the list is read to decide what to do next, and
// burying the two live items under eleven completed ones answers a question
// nobody asked.
func todoView(items []session.TodoItem, width int) []string {
	if len(items) == 0 {
		return nil
	}
	var open, settled []session.TodoItem
	for _, it := range items {
		if todoSettled(it.State) {
			settled = append(settled, it)
		} else {
			open = append(open, it)
		}
	}

	var lines []string
	for _, it := range open {
		lines = append(lines, todoRow(it, width)...)
	}
	if len(open) > 0 && len(settled) > 0 {
		lines = append(lines, "")
	}
	for _, it := range settled {
		lines = append(lines, todoRow(it, width)...)
	}
	return lines
}

// todoRow renders one task: its state, id, text and author, then any notes
// hanging beneath it in an aligned column.
func todoRow(it session.TodoItem, width int) []string {
	head := fmt.Sprintf("%s %s  %s", todoGlyph(it.State), todoID2(it.ID), it.Text)

	// The author sits at the right edge rather than inline. Inline it competed
	// with the task text for the eye; at the edge it forms a column you can
	// read down to see how much of the plan is whose.
	//
	// A task whose text already reaches the edge keeps its text: truncating
	// what the task SAYS to make room for who wrote it has the priority
	// backwards.
	if it.Origin.ByAgent() {
		if shell.Columns(head)+len("helix")+1 <= width {
			head = shell.PadColumns(head, width-len("helix")) + "helix"
		} else {
			head += "  helix"
		}
	}

	lines := []string{head}
	// Sub-lines are indented to the text column and labelled, so "you wrote"
	// and "why" line up into a scannable second column instead of running on
	// as prose.
	if it.Revised() {
		lines = append(lines, todoNote("you wrote", it.WasText, width)...)
	}
	if it.Reason != "" {
		lines = append(lines, todoNote("why", it.Reason, width)...)
	}
	if it.Note != "" {
		lines = append(lines, todoNote("note", it.Note, width)...)
	}
	return lines
}

// todoNoteIndent is the text column: state glyph (1) + space + id (2) + two
// spaces. Sub-lines hang under the task text, not under its number.
const todoNoteIndent = "      "

// todoNoteLabel is the label column: "↳ " (2) + the longest label, "you wrote"
// (9), + one space so the value never touches its label. Derived rather than
// guessed, because an off-by-one here is invisible in code and obvious on
// screen — the first draft used 11 and put "you wrote" one column right of
// every other label.
const todoNoteLabel = 2 + len("you wrote") + 1

// todoNote renders one labelled sub-line, wrapped with its continuations hung
// under the value.
//
// Measured in COLUMNS, not bytes. "↳" is three bytes and one column, so a
// len()-based pad puts "you wrote" and "why" at different left edges and the
// second column stops being a column — which is the whole reason for the
// labels. This repo has made that mistake before; the fix is the same one.
//
// Continuations carry the indent but NOT the arrow. Repeating "↳" down a
// wrapped sentence reads as a list of separate notes; one marker with its text
// flowing under it reads as one.
func todoNote(label, value string, width int) []string {
	avail := width - shell.Columns(todoNoteIndent) - todoNoteLabel
	if avail < 24 {
		avail = 24
	}
	wrapped := wrapPlain(value, avail)
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		marker := "↳ " + label
		if i > 0 {
			marker = ""
		}
		out = append(out, todoNoteIndent+shell.PadColumns(marker, todoNoteLabel)+line)
	}
	return out
}

// todoMeter is the one-line summary: a filled bar and only the counts that are
// not zero.
//
// Showing every state including the empty ones turned a fresh three-task list
// into five facts, four of them "0". A count of nothing is not information.
func todoMeter(items []session.TodoItem, width int) string {
	if len(items) == 0 {
		return ""
	}
	counts := map[session.TodoState]int{}
	done := 0
	for _, it := range items {
		counts[it.State]++
		if todoSettled(it.State) {
			done++
		}
	}

	const barCells = 16
	filled := done * barCells / len(items)
	bar := strings.Repeat("▓", filled) + strings.Repeat("░", barCells-filled)

	order := []struct {
		state session.TodoState
		label string
	}{
		{session.TodoInProgress, "in progress"},
		{session.TodoPending, "pending"},
		{session.TodoBlocked, "blocked"},
		{session.TodoDone, "done"},
		{session.TodoSuperseded, "superseded"},
	}
	var parts []string
	for _, o := range order {
		if n := counts[o.state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, o.label))
		}
	}
	return bar + "  " + strings.Join(parts, " · ") + "   " + todoHeading(items)
}

// todoHeading is the settled count, rendered at the END of the meter line.
//
// It was its own line at first and that was one line too many: the bar already
// shows the proportion and the counts already show the parts, so a third
// rendering of the same fact is noise wearing a label.
func todoHeading(items []session.TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	done := 0
	for _, it := range items {
		if todoSettled(it.State) {
			done++
		}
	}
	return fmt.Sprintf("%d of %d settled", done, len(items))
}

// todoSettled reports whether a state means the task needs no more attention.
// Done and superseded are both finished; the second means the agent decided it
// should not happen, which is just as settled as having done it.
func todoSettled(s session.TodoState) bool {
	return s == session.TodoDone || s == session.TodoSuperseded
}

// todoGlyph is the state marker. Every state gets its own, because one shared
// bullet for "pending, in progress and blocked" made the live item — the only
// one worth looking for — indistinguishable from the queue behind it.
func todoGlyph(s session.TodoState) string {
	switch s {
	case session.TodoDone:
		return "✔"
	case session.TodoInProgress:
		return "▸"
	case session.TodoBlocked:
		return "✖"
	case session.TodoSuperseded:
		return "⊘"
	default:
		return "·"
	}
}

// todoID2 renders an id in a fixed two-column field so the text starts at the
// same place on every row. Ids past 99 push the column rather than truncate:
// a misaligned row is better than a task you cannot address.
func todoID2(id int) string {
	return fmt.Sprintf("%2d", id)
}

// wrapPlain wraps uncoloured text at a width, breaking on spaces.
func wrapPlain(text string, width int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := words[0]
	for _, w := range words[1:] {
		if len(cur)+1+len(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		cur += " " + w
	}
	return append(lines, cur)
}

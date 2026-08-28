// cmd/helix/ui.go
//
// Purpose: the shapes that repeat across fifty-seven commands, written once.
//
// internal/shell owns the visual language — panels, badges, KV rows, steps.
// What it cannot own is the ARRANGEMENT a given kind of screen wants, and three
// arrangements account for most of the command surface:
//
//   - a toggle reporting or changing its state (/audio, /agentic, /dry-run…),
//   - a short labelled report (/version, /crash, /history…),
//   - a one-line outcome (done, refused, failed).
//
// Before this, each of those was written out by hand at every call site, which
// is why fifty-odd commands drifted into fifty-odd slightly different screens.
// The helpers here are deliberately thin: they compose shell primitives and add
// no styling of their own, so there is still exactly one visual language and
// only one place that decides what it looks like.
package main

import (
	"fmt"

	"helix/internal/shell"
)

// uiToggle renders a two-state setting, with or without a change.
//
// The single most repeated screen in the shell, and it had at least four
// spellings: "Audio is currently: ON (READY)", "Typewrite-all mode is
// currently: OFF", "Agentic mode ENABLED", plus a bare "Usage:" line in some
// and not others. Same information, four shapes, so a reader learned nothing
// from the first that helped with the second.
//
// Args:
//   - name: the row label, e.g. "AUDIO".
//   - on: current state.
//   - onText/offText: what each state MEANS, not just the word — "spoken
//     aloud" beats "ON", because the state word is already carried by the
//     badge colour.
//   - usage: the command form that changes it, or "" to omit the hint.
func uiToggle(name string, on bool, onText, offText, usage string) {
	state := shell.Badge(shell.StateIdle, "off")
	if on {
		state = shell.Badge(shell.StateGood, "on")
	}
	detail := offText
	if on {
		detail = onText
	}
	if detail != "" {
		state += shell.Muted("  " + detail)
	}
	fmt.Println(shell.KV(name, state, shell.KVWidth(name)))
	if usage != "" {
		fmt.Println(shell.Hint(usage))
	}
}

// uiOK, uiWarn and uiFail are one-line outcomes.
//
// A command that did the thing, could not, or half did. Separate functions
// rather than a state parameter because the call sites read better and the
// state is then impossible to pass wrongly.
func uiOK(subject, detail string)   { fmt.Println(shell.Step(shell.StateGood, subject, detail)) }
func uiWarn(subject, detail string) { fmt.Println(shell.Step(shell.StateWarn, subject, detail)) }
func uiFail(subject, detail string) { fmt.Println(shell.Step(shell.StateBad, subject, detail)) }

// uiIdle is the fourth outcome: nothing happened, and that is fine.
//
// Distinct from uiWarn on purpose. "No crash reports" and "the update was not
// installed" are not problems, and rendering them in the warning colour is how
// a screen full of yellow trains people to stop reading yellow.
func uiIdle(subject, detail string) { fmt.Println(shell.Step(shell.StateIdle, subject, detail)) }

// uiDetail prints the explanation belonging to the line above it.
func uiDetail(text string) {
	for _, l := range shell.StepDetail(text, shell.Muted) {
		fmt.Println(l)
	}
}

// uiRow is one row of a uiReport.
type uiRow struct {
	Label string
	Value string // already coloured, or plain
}

// uiReport renders a titled panel of aligned rows.
//
// The column width is computed from every label in the set, which is the whole
// reason this exists as a helper: a caller that computes it from a subset gets
// a column that drifts as rows come and go, and that was already happening.
func uiReport(title string, rows ...uiRow) {
	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, r.Label)
	}
	w := shell.KVWidth(labels...)

	fmt.Println(shell.PanelTitle(title))
	for _, r := range rows {
		fmt.Println(shell.KV(r.Label, r.Value, w))
	}
	fmt.Println(shell.PanelEnd())
}

// uiUsage is the "you typed it wrong" reply.
//
// One shape for every command, because the old ones ranged from a bare
// `Usage: /audio <on|off>` in the warning colour to nothing at all, and a
// misuse message that looks like an error teaches the user that they broke
// something when they merely mistyped.
func uiUsage(form string, notes ...string) {
	fmt.Println(shell.Hint(form))
	for _, n := range notes {
		uiDetail(n)
	}
}

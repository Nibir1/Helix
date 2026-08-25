// cmd/helix/blackbox_status_width_test.go
// Purpose: status rows must stay readable at the narrowest panel.
//
// shell.KV now wraps and hangs continuation lines under the value column, so an
// over-wide value is no longer a rendering BUG — it used to wrap at the terminal
// edge and restart at column zero, outside the gutter, and /blackbox status
// carried one such line 95 columns wide.
//
// What is left is a readability budget rather than a correctness one, and it is
// still worth holding. A summary panel exists to be scanned; if every row runs
// to two lines it has stopped being a summary. So the states seen in ordinary
// operation must fit on one line, while the diagnostic states — which say what
// went wrong and how to fix it, and are worth the room — may take a second.
//
// Everything is measured against the NARROWEST panel (a 72-column rule): a wide
// terminal merely hides the question.
package main

import (
	"regexp"
	"strings"
	"testing"

	"helix/internal/shell"
	"helix/internal/speech"
)

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth is the printed width, with the colour escapes removed.
func visibleWidth(s string) int {
	return len([]rune(ansiSeq.ReplaceAllString(s, "")))
}

// statusRowBudget is what a value may occupy in the /blackbox status panel:
// the 72-column rule plus its 2-column indent, less the gutter and the label
// column that shell.KV puts in front of every value.
func statusRowBudget(t *testing.T) int {
	t.Helper()
	// PanelLine prefix is "  " + gutter + " " = 4 columns; KV then pads the
	// label out to the column width and adds 2 more.
	return (72 + 2) - (4 + statusLabelWidth() + 2)
}

// statusLabelWidth is the label column /blackbox status renders with.
func statusLabelWidth() int {
	return shell.KVWidth("MODE", "HEARING", "SIGHT", "WAKE",
		"INITIATIVE", "CONTEXT", "TRANSCRIPT")
}

func TestContextLineStaysReadable(t *testing.T) {
	budget := statusRowBudget(t)

	// Values chosen to be the widest each branch can realistically render:
	// the longest provider name in the chain, and a full context buffer.
	cases := []struct {
		name string
		// oneLine marks the states seen in ordinary operation. The diagnostic
		// states may wrap: telling someone their sidecar is silently discarding
		// context, and naming the patch that fixes it, is worth a second line.
		oneLine bool
		rep     speech.ContextReport
	}{
		{"disabled, no capable provider", true, speech.ContextReport{}},
		{"disabled, capable provider", true, speech.ContextReport{Provider: "csm-local"}},
		{"honored", true, speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20,
			Attempted: true, Honored: true}},
		{"awaiting the first reply", true, speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20}},
		{"retained but unusable", false, speech.ContextReport{
			Enabled: true, Turns: 4, Bytes: 4 << 20}},
		{"refused", false, speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20,
			Attempted: true, Rejected: true}},
		{"ignored by an unpatched sidecar", false, speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20,
			Attempted: true, Ignored: true}},
	}

	for _, tc := range cases {
		line := blackBoxContextLine(tc.rep)
		if line == "" {
			t.Errorf("%s: produced no line at all", tc.name)
			continue
		}
		if tc.oneLine && visibleWidth(line) > budget {
			t.Errorf("%s is an everyday state and must fit one line: %d columns, budget %d\n  %s",
				tc.name, visibleWidth(line), budget, ansiSeq.ReplaceAllString(line, ""))
		}
		// Even a diagnostic may not sprawl: two rendered lines, never three.
		rendered := shell.KV("CONTEXT", line, statusLabelWidth())
		if n := strings.Count(rendered, "\n") + 1; n > 2 {
			t.Errorf("%s renders as %d lines, too much for a summary row:\n%s",
				tc.name, n, ansiSeq.ReplaceAllString(rendered, ""))
		}
	}
}

// The distinction the header detection was built for must actually reach the
// screen: a sidecar that silently drops context must not read as success.
func TestContextLineDistinguishesIgnoredFromHonored(t *testing.T) {
	base := speech.ContextReport{
		Enabled: true, Provider: "csm-local", Turns: 2, Bytes: 1 << 20, Attempted: true}

	honored := base
	honored.Honored = true
	ignored := base
	ignored.Ignored = true

	if a, b := blackBoxContextLine(honored), blackBoxContextLine(ignored); a == b {
		t.Fatal("an ignored context must not render identically to an honored one")
	}
	if got := ansiSeq.ReplaceAllString(blackBoxContextLine(ignored), ""); !strings.Contains(got, "patch") {
		t.Errorf("the ignored line should say how to fix it, got %q", got)
	}
	// Retention with nothing able to use it is a privacy cost worth naming.
	unusable := blackBoxContextLine(speech.ContextReport{Enabled: true, Turns: 3})
	if got := ansiSeq.ReplaceAllString(unusable, ""); !strings.Contains(got, "unused") {
		t.Errorf("retained-but-unusable context must say so, got %q", got)
	}
}

// The usage block is a hand-aligned column, which means it drifts by one space
// and nobody notices until it is on screen — `/blackbox look [question]` sat a
// column right of every other description for exactly that reason. Alignment is
// the whole reason to write it as a padded table rather than prose.
func TestBlackBoxUsageIsAligned(t *testing.T) {
	col := -1
	for _, line := range blackBoxUsage {
		if strings.TrimSpace(line) == "" {
			continue
		}
		gap := strings.Index(line, "  ")
		if gap < 0 {
			t.Errorf("usage line has no description column: %q", line)
			continue
		}
		// The description starts at the first non-space after the first run of
		// two or more spaces.
		start := gap
		for start < len(line) && line[start] == ' ' {
			start++
		}
		if col == -1 {
			col = start
		} else if start != col {
			t.Errorf("description starts at column %d, others at %d: %q", start, col, line)
		}
		// 80 columns is the terminal these are printed into; the block is not
		// inside a panel, so nothing wraps it for us.
		if len(line) > 84 {
			t.Errorf("usage line is %d columns, too wide to read at 80: %q", len(line), line)
		}
	}
}

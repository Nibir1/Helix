// cmd/helix/blackbox_status_width_test.go
// Purpose: status rows must fit inside the panel that frames them.
//
// shell.KV neither wraps nor truncates. A value wider than the rule therefore
// wraps at the TERMINAL edge instead, and its tail restarts at column zero —
// outside the gutter, visually detached from the block it belongs to. That is
// the defect shell.PanelWrap exists to prevent for prose, and nothing was
// preventing it for KV rows: when /blackbox status first grew a CONTEXT row,
// five of its seven branches overflowed, as did the camera's "no frames" line
// at 95 columns into a 74-column row. All of them read fine in source.
//
// The budget is computed for the NARROWEST panel (a 72-column rule), because
// that is the case that breaks; a wide terminal merely hides the bug.
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
	w := shell.KVWidth("MODE", "HEARING", "SIGHT", "WAKE", "INITIATIVE", "CONTEXT", "TRANSCRIPT")
	// PanelLine prefix is "  " + gutter + " " = 4 columns; KV then pads the
	// label out to w and adds 2 more.
	return (72 + 2) - (4 + w + 2)
}

func TestContextLineFitsThePanel(t *testing.T) {
	budget := statusRowBudget(t)

	// Values chosen to be the widest each branch can realistically render:
	// the longest provider name in the chain, and a full context buffer.
	cases := []struct {
		name string
		rep  speech.ContextReport
	}{
		{"disabled, no capable provider", speech.ContextReport{}},
		{"disabled, capable provider", speech.ContextReport{Provider: "csm-local"}},
		{"retained but unusable", speech.ContextReport{Enabled: true, Turns: 4, Bytes: 4 << 20}},
		{"refused", speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20,
			Attempted: true, Rejected: true}},
		{"ignored by an unpatched sidecar", speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20,
			Attempted: true, Ignored: true}},
		{"honored", speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20,
			Attempted: true, Honored: true}},
		{"awaiting the first reply", speech.ContextReport{
			Enabled: true, Provider: "csm-local", Turns: 4, Bytes: 4 << 20}},
	}

	for _, tc := range cases {
		line := blackBoxContextLine(tc.rep)
		if got := visibleWidth(line); got > budget {
			t.Errorf("%s: %d columns exceeds the %d-column budget:\n  %s",
				tc.name, got, budget, ansiSeq.ReplaceAllString(line, ""))
		}
		if line == "" {
			t.Errorf("%s: produced no line at all", tc.name)
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

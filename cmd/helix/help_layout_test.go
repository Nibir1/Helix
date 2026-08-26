// cmd/helix/help_layout_test.go
// Purpose: /help is the densest screen Helix draws and the one a confused user
// reaches for first, so its layout is pinned rather than eyeballed.
//
// The defects this replaces were both invisible in source. The index padded
// commands into a fixed 30-column gutter and clamped the pad at 2 when a usage
// line was longer, so nine of fifty-six commands started their description at a
// different column — which is what read as overlapping text. And nothing
// wrapped: the widest row was 124 columns against a hardcoded 76-column rule,
// so it broke at the terminal edge and restarted outside the gutter.
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"helix/internal/shell"
)

var helpANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				b.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		done <- b.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// Nothing may leave the frame. The rule is drawn after a 2-column indent, so a
// line may occupy at most panelWidth+2 visible columns.
func TestHelpStaysInsideThePanel(t *testing.T) {
	out := captureStdout(t, func() { handleHelp(cmdArgs{}) })
	if !strings.Contains(out, "/blackbox") {
		t.Fatal("captured no help output")
	}
	limit := shell.PanelRuleWidth() + 2
	for i, line := range strings.Split(out, "\n") {
		plain := helpANSI.ReplaceAllString(line, "")
		if w := len([]rune(plain)); w > limit {
			t.Errorf("help line %d is %d columns, past the %d-column rule:\n  %s",
				i, w, limit, plain)
		}
	}
}

// Every command description must start at the SAME column. This is the
// assertion the old fixed-gutter code could not satisfy.
func TestHelpDescriptionsShareOneColumn(t *testing.T) {
	out := captureStdout(t, func() { handleHelp(cmdArgs{}) })

	col, checked := -1, 0
	for _, line := range strings.Split(out, "\n") {
		plain := helpANSI.ReplaceAllString(line, "")
		g := strings.Index(plain, "│")
		if g < 0 {
			continue
		}
		body := plain[g+len("│"):]
		trimmed := strings.TrimLeft(body, " ")
		// Only the command rows: they start with a slash after the gutter.
		if !strings.HasPrefix(trimmed, "/") {
			continue
		}
		fields := strings.SplitN(trimmed, "  ", 2)
		if len(fields) != 2 || strings.TrimSpace(fields[1]) == "" {
			continue // a command whose description wrapped away, or none
		}
		// Where the description begins: past the leading gutter space, past the
		// command, then past the padding that aligns the column.
		start := len([]rune(body)) - len([]rune(strings.TrimLeft(body, " "))) +
			len([]rune(fields[0]))
		for start < len([]rune(body)) && []rune(body)[start] == ' ' {
			start++
		}
		checked++
		if col == -1 {
			col = start
		} else if start != col {
			t.Errorf("description starts at column %d, others at %d:\n  %s",
				start, col, plain)
		}
	}
	if checked < 20 {
		t.Fatalf("only checked %d command rows; the parse is wrong", checked)
	}
}

// The index lists NAMES; argument syntax belongs to /help <command>, which has
// the whole width for it. Pinned because the reverse is what made the index
// unreadable.
func TestHelpIndexListsNamesNotSignatures(t *testing.T) {
	out := helpANSI.ReplaceAllString(captureStdout(t, func() { handleHelp(cmdArgs{}) }), "")
	if !strings.Contains(out, "/blackbox") {
		t.Fatal("the index must list /blackbox")
	}
	if strings.Contains(out, "on|off|status|setup") {
		t.Error("the index must not carry a 64-column signature; that is /help <command>'s job")
	}
	if !strings.Contains(out, "/help <command>") {
		t.Error("the index must say where the arguments are")
	}
}

// The unknown-command screen is the most-printed error in the shell, and there
// used to be two of them: "/nosuch" drew a red header with gutter bars and no
// panel around them, while "/help nosuch" drew two bare indented lines with no
// gutter at all. Same mistake, two presentations, neither framed.
func TestUnknownCommandIsOneScreen(t *testing.T) {
	typed := captureStdout(t, func() { printUnknownCommand("/nosuchthing") })
	viaHelp := captureStdout(t, func() { printCommandDetail("/nosuchthing") })
	if typed != viaHelp {
		t.Errorf("the same unknown command renders differently by route:\n--- typed:\n%s\n--- /help:\n%s",
			typed, viaHelp)
	}
	if !strings.Contains(helpANSI.ReplaceAllString(typed, ""), "UNKNOWN COMMAND") {
		t.Error("the screen should name what happened")
	}
	limit := shell.PanelRuleWidth() + 2
	for _, line := range strings.Split(typed, "\n") {
		plain := helpANSI.ReplaceAllString(line, "")
		if w := len([]rune(plain)); w > limit {
			t.Errorf("line is %d columns, past the %d-column rule: %s", w, limit, plain)
		}
	}
}

// Every suggestion must be coloured, not just the first.
//
// The natural-looking construction is wrong in a way that is invisible in
// source: shell.Value(strings.Join(items, shell.Muted(sep))) puts a colour
// RESET inside the coloured span, so the separator ends it and every suggestion
// after the first renders plain.
func TestAllSuggestionsAreColoured(t *testing.T) {
	// "/ra" matches the three rag-* commands.
	out := captureStdout(t, func() { printUnknownCommand("/ra") })

	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(helpANSI.ReplaceAllString(line, ""), "DID YOU MEAN") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("expected a suggestion row for /ra")
	}
	names := 0
	for _, seg := range strings.Split(row, "\x1b[0m") {
		if strings.Contains(seg, "/rag-") && strings.Contains(seg, "\x1b[") {
			names++
		}
	}
	if names < 3 {
		t.Errorf("only %d of 3 suggestions carry their own colour: %q", names, row)
	}
}

// Pointing at /help twice is noise when /help IS the suggestion — which is
// exactly what a mistyped "/hel" produces.
func TestUnknownCommandDoesNotRepeatHelp(t *testing.T) {
	out := helpANSI.ReplaceAllString(captureStdout(t, func() { printUnknownCommand("/hel") }), "")
	if strings.Count(out, "/help") != 1 {
		t.Errorf("/help should appear once when it is the suggestion:\n%s", out)
	}
}

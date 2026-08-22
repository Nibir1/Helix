// internal/shell/panel_test.go
// Purpose: the panel primitives exist to stop alignment and containment bugs
// being re-invented per command, so the properties worth pinning are the ones
// that broke before: colour must not be counted as width, and a long line must
// not escape the block it belongs to.
package shell

import (
	"strings"
	"testing"
)

// The bug this file exists to prevent: fmt's %-9s counts ANSI escape bytes, so
// padding a coloured cell pads to nothing visible and the column collapses.
func TestPaddingMeasuresVisibleWidthNotBytes(t *testing.T) {
	plain := PadVisible("ready", 9)
	coloured := PadVisible(Fg(HexPrimary, "ready"), 9)

	if visibleWidth(plain) != 9 {
		t.Errorf("plain padded to %d visible cells, want 9", visibleWidth(plain))
	}
	if visibleWidth(coloured) != 9 {
		t.Errorf("coloured padded to %d visible cells, want 9 — escapes were counted",
			visibleWidth(coloured))
	}
	// Already too wide: never truncate, never pad negative.
	long := PadVisible("a-very-long-cell", 4)
	if !strings.HasPrefix(long, "a-very-long-cell") {
		t.Errorf("padding must not truncate: %q", long)
	}
}

func TestTableColumnsAlignWithColouredCells(t *testing.T) {
	rows := [][]string{
		{Fg(HexTertiary, "groq"), Muted("Groq Whisper Turbo"), Badge(StateGood, "healthy")},
		{Fg(HexTertiary, "whisper-local"), Muted("Whisper (local sidecar)"), Badge(StateBad, "down")},
	}
	out := Table([]string{"provider", "name", "state"}, rows)
	if len(out) != 3 {
		t.Fatalf("got %d lines, want header + 2 rows", len(out))
	}

	// The second column must start at the same visible offset on every row,
	// which is the whole point of computing widths from content.
	offsets := make([]int, 0, len(out))
	for _, line := range out {
		plain := ansiRegex.ReplaceAllString(line, "")
		idx := strings.Index(plain, "  ")
		if idx < 0 {
			t.Fatalf("no column gap in %q", plain)
		}
		offsets = append(offsets, idx)
	}
	// The widest first-column cell is "whisper-local"; every row's gap must
	// begin at or after the header's, never before it.
	for i, o := range offsets {
		if o < offsets[0]-1 {
			t.Errorf("row %d column gap at %d, header at %d — columns drifted", i, o, offsets[0])
		}
	}
}

// A sentence longer than the panel must stay inside the gutter. Before
// PanelWrap the endpoint-conflict note wrapped at the terminal edge and its
// tail restarted at column zero, visually leaving the block.
func TestPanelWrapKeepsEveryLineBehindTheGutter(t *testing.T) {
	long := strings.Repeat("port collision on 127.0.0.1:8080 and more words ", 6)
	lines := PanelWrap(long, nil)
	if len(lines) < 2 {
		t.Fatalf("a %d-char sentence produced %d lines", len(long), len(lines))
	}
	for i, l := range lines {
		plain := ansiRegex.ReplaceAllString(l, "")
		if !strings.HasPrefix(plain, "  "+glyphGutter+" ") {
			t.Errorf("line %d is not behind the gutter: %q", i, plain)
		}
		if visibleWidth(plain) > panelWidth()+2 {
			t.Errorf("line %d is %d cells, wider than the panel", i, visibleWidth(plain))
		}
	}
	if PanelWrap("   ", nil) != nil {
		t.Error("whitespace-only prose should produce no lines")
	}
}

// A state must never be able to render in a colour that contradicts it — the
// class of bug that had a camera reporting "ready" with no capture pipeline.
func TestBadgesCarryDistinctColours(t *testing.T) {
	seen := map[string]State{}
	for _, s := range []State{StateGood, StateWarn, StateBad, StateIdle} {
		out := Badge(s, "x")
		prefix := out[:strings.Index(out, "x")]
		if other, dup := seen[prefix]; dup {
			t.Errorf("states %v and %v render identically — they cannot be told apart", other, s)
		}
		seen[prefix] = s
	}
	if !strings.Contains(Badge(StateBad, "down"), "down") {
		t.Error("a badge must still contain its text")
	}
}

func TestKVAlignsOnTheWidestLabel(t *testing.T) {
	w := KVWidth("MODE", "INITIATIVE", "SIGHT")
	if w != len("INITIATIVE") {
		t.Fatalf("width = %d, want the widest label", w)
	}
	short := ansiRegex.ReplaceAllString(KV("MODE", "on", w), "")
	long := ansiRegex.ReplaceAllString(KV("INITIATIVE", "on", w), "")
	if strings.Index(short, "on") != strings.Index(long, "on") {
		t.Errorf("values start at different columns:\n%q\n%q", short, long)
	}
}

// The rule width has to survive a hostile terminal size rather than emit a
// negative repeat count (a panic) or a 200-column horizon.
func TestPanelWidthIsClamped(t *testing.T) {
	if w := panelWidth(); w < 52 || w > 92 {
		t.Errorf("panel width %d escaped its clamp", w)
	}
}

// A table that overflows the panel wraps at the terminal edge and its tail
// restarts at column zero, which destroys the grid it exists to be. Found by
// screenshotting: the nine-column pricing table pushed "★ best value" onto its
// own line outside the gutter.
func TestTableRowsFitInsideThePanel(t *testing.T) {
	// A realistic worst case: the longest real provider and model names.
	rows := [][]string{
		{Muted(" 5)"), Value("elevenlabs"), Fg(HexText, "eleven_multilingual_v2"),
			Value("$229.50"), Muted("very_low"), Muted("api key · cloud"), Fg(HexSecondary, "★")},
		{Muted(" 7)"), Value("whisper-local"), Fg(HexText, "whisper-large-v3-turbo"),
			Fg(HexPrimary, "free"), Muted("medium"), Fg(HexPrimary, "nothing · local"), ""},
	}
	for _, line := range Table([]string{"", "provider", "model", "$/month", "latency", "requires", ""}, rows) {
		if w := visibleWidth(line); w > panelWidth()+2 {
			t.Errorf("row is %d cells wide, panel is %d — it will wrap out of the gutter:\n%s",
				w, panelWidth(), ansiRegex.ReplaceAllString(line, ""))
		}
	}
}

// Colour must survive a truncation, and a cut must never sever an escape
// sequence — a half-written sequence bleeds its colour across the rest of the
// line and there is nothing to close it.
func TestTruncateANSIKeepsColourIntact(t *testing.T) {
	coloured := Fg(HexTertiary, "eleven_multilingual_v2")
	cut := truncateANSI(coloured, 8)

	if visibleWidth(cut) > 8 {
		t.Errorf("truncated to %d visible cells, want ≤8: %q", visibleWidth(cut), cut)
	}
	if !strings.Contains(cut, "…") {
		t.Error("a truncated cell should say so")
	}
	if !strings.HasSuffix(cut, ansiReset) {
		t.Error("a truncated cell must still close its colour")
	}
	// The visible text is a prefix of the original's visible text.
	plainCut := ansiRegex.ReplaceAllString(cut, "")
	if !strings.HasPrefix("eleven_multilingual_v2", strings.TrimSuffix(plainCut, "…")) {
		t.Errorf("truncation changed the text: %q", plainCut)
	}
	// Short enough already: returned untouched, no stray ellipsis.
	if got := truncateANSI(coloured, 100); got != coloured {
		t.Error("a cell that already fits must not be altered")
	}
}

func TestFitTableWidthsRespectsAFloor(t *testing.T) {
	widths := []int{40, 40, 40}
	fitTableWidths(widths, 20)
	for i, w := range widths {
		if w < 4 {
			t.Errorf("column %d shrank to %d, below the readable floor", i, w)
		}
	}
	// Impossible targets must terminate rather than spin.
	widths = []int{4, 4}
	fitTableWidths(widths, 1)
	if widths[0] != 4 || widths[1] != 4 {
		t.Error("columns already at the floor must be left alone")
	}
}

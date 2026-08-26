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

// A value that fits must be emitted exactly as it was before KV learned to
// wrap. Every panel in the app renders through this, so a change in the common
// case would be a change everywhere at once.
func TestKVLeavesFittingValuesAlone(t *testing.T) {
	w := KVWidth("MODE", "INITIATIVE")
	value := Badge(StateGood, "LIVE") + Muted("  listening")

	got := KV("MODE", value, w)
	if strings.Contains(got, "\n") {
		t.Errorf("a value that fits must stay on one line, got %q", got)
	}
	// HexMuted, not HexSubtle: the label is TEXT, so it moved to the readable
	// tone when the palette split chrome from text.
	if want := PanelLine(Fg(HexMuted, "MODE") +
		strings.Repeat(" ", w-len("MODE")+2) + value); got != want {
		t.Errorf("fitting output changed:\n got %q\nwant %q", got, want)
	}
}

// The defect this exists for: KV emitted one line whatever its length, so an
// over-wide value wrapped at the TERMINAL edge and its tail restarted at column
// zero, outside the gutter. /blackbox status carried one 95 columns wide.
func TestKVWrapsInsideThePanel(t *testing.T) {
	w := KVWidth("MODE", "INITIATIVE", "TRANSCRIPT")
	value := Badge(StateBad, "no frames") + Muted("  camera opens but delivers "+
		"nothing — likely an OS permission; /blackbox look shows why")

	lines := strings.Split(KV("SIGHT", value, w), "\n")
	if len(lines) < 2 {
		t.Fatalf("an over-wide value must wrap, got one line: %q", lines[0])
	}
	// +2 for the indent PanelLine adds before the gutter.
	limit := panelWidth() + 2
	for i, l := range lines {
		if got := visibleWidth(l); got > limit {
			t.Errorf("line %d is %d columns, past the %d-column rule: %q",
				i, got, limit, ansiRegex.ReplaceAllString(l, ""))
		}
		if !strings.Contains(l, glyphGutter) {
			t.Errorf("line %d escaped the gutter: %q", i, l)
		}
	}

	// Continuation lines hang under the value column, not under the label.
	plain := ansiRegex.ReplaceAllString(lines[1], "")
	body := plain[strings.Index(plain, glyphGutter)+len(glyphGutter)+1:]
	if got := len(body) - len(strings.TrimLeft(body, " ")); got != w+2 {
		t.Errorf("continuation indent = %d, want %d (the value column)", got, w+2)
	}
}

// Wrapping must not break the words it wraps, and must not lose them.
func TestKVWrapPreservesTextAndBreaksOnWords(t *testing.T) {
	w := KVWidth("LABEL")
	value := "alpha bravo charlie delta echo foxtrot golf hotel india juliet " +
		"kilo lima mike november oscar papa quebec romeo sierra tango"

	lines := strings.Split(KV("LABEL", value, w), "\n")
	var words []string
	for _, l := range lines {
		plain := ansiRegex.ReplaceAllString(l, "")
		// Every line carries the gutter; only the first carries the label.
		plain = plain[strings.Index(plain, glyphGutter)+len(glyphGutter):]
		words = append(words, strings.Fields(plain)...)
	}
	words = words[1:] // the label on the first line
	if got, want := strings.Join(words, " "), value; got != want {
		t.Errorf("wrapping altered the text:\n got %q\nwant %q", got, want)
	}
}

// A token longer than the column has no break point, so it must be split rather
// than allowed to overflow — a long path or URL is the realistic case.
func TestKVWrapHardBreaksAnUnbreakableToken(t *testing.T) {
	w := KVWidth("LABEL")
	lines := strings.Split(KV("LABEL", strings.Repeat("x", 200), w), "\n")
	if len(lines) < 3 {
		t.Fatalf("a 200-column token must split across lines, got %d", len(lines))
	}
	total := 0
	for _, l := range lines {
		if got := visibleWidth(l); got > panelWidth()+2 {
			t.Errorf("hard-broken line still overflows at %d columns", got)
		}
		total += strings.Count(l, "x")
	}
	if total != 200 {
		t.Errorf("hard break lost characters: %d of 200 survived", total)
	}
}

// Colour must survive a break in both directions: the sequence in effect at the
// end of a line is closed there and reopened on the next. Without the reopen a
// wrapped value renders coloured then plain; without the close it bleeds into
// the gutter glyph of the line beneath.
func TestWrapANSICarriesColourAcrossBreaks(t *testing.T) {
	value := Muted(strings.Repeat("word ", 40))
	lines := wrapANSI(value, 30)
	if len(lines) < 2 {
		t.Fatal("expected several lines")
	}
	for i, l := range lines {
		if !strings.HasPrefix(l, "\x1b") {
			t.Errorf("line %d does not reopen the active colour: %q", i, l)
		}
		if !strings.HasSuffix(l, ansiReset) {
			t.Errorf("line %d does not close its colour: %q", i, l)
		}
		if got := visibleWidth(l); got > 30 {
			t.Errorf("line %d is %d visible columns, want <= 30", i, got)
		}
	}
}

// Escapes are zero-width: a heavily coloured value must not wrap earlier than
// the same text uncoloured, which is what counting escape bytes would cause.
func TestWrapANSIDoesNotCountEscapes(t *testing.T) {
	plain := "alpha bravo charlie delta echo foxtrot golf hotel india"
	var coloured string
	for i, word := range strings.Fields(plain) {
		if i > 0 {
			coloured += " "
		}
		coloured += Value(word)
	}
	if got, want := len(wrapANSI(coloured, 20)), len(wrapANSI(plain, 20)); got != want {
		t.Errorf("coloured text wrapped into %d lines, plain into %d", got, want)
	}
}

// A width narrower than a single cell must still terminate rather than spin.
func TestWrapANSIMakesProgressAtAbsurdWidths(t *testing.T) {
	for _, w := range []int{-1, 0, 1, 2} {
		lines := wrapANSI(Value("hello world"), w)
		if len(lines) == 0 {
			t.Errorf("width %d produced no output", w)
		}
	}
}

// PanelWrap measured with len(), which counts a multi-byte rune once per byte.
// This codebase's panels are full of "·", "—" and "→" (2-3 bytes each, one
// column each), so prose carrying them wrapped early — breaking lines that had
// room. Never past the frame, but wrong, and the same class of bug as measuring
// ANSI escapes as content.
func TestPanelWrapMeasuresColumnsNotBytes(t *testing.T) {
	// Same column width per word, very different byte counts: "ab" is 2 bytes
	// wide and 2 columns; "——" is 6 bytes and 2 columns.
	ascii := strings.TrimSpace(strings.Repeat("ab ", 40))
	multi := strings.TrimSpace(strings.Repeat("—— ", 40))

	got, want := len(PanelWrap(multi, nil)), len(PanelWrap(ascii, nil))
	if want < 2 {
		t.Fatalf("precondition: the ASCII case must wrap, got %d lines", want)
	}
	if got != want {
		t.Errorf("multi-byte prose wrapped into %d lines, equivalent ASCII into %d",
			got, want)
	}
	for i, l := range PanelWrap(multi, nil) {
		plain := ansiRegex.ReplaceAllString(l, "")
		if visibleWidth(plain) > panelWidth()+2 {
			t.Errorf("line %d overflows the panel at %d columns: %q",
				i, visibleWidth(plain), plain)
		}
	}
}

// A word longer than the panel has no break point, so PanelWrap emitted it as
// its own over-wide line — letting content escape the very frame it exists to
// keep it inside. A URL or an absolute path is the realistic case.
func TestPanelWrapSplitsAnUnbreakableWord(t *testing.T) {
	url := "https://example.invalid/" + strings.Repeat("segment/", 20)
	lines := PanelWrap("the endpoint is "+url+" which is unreachable", nil)
	if len(lines) < 2 {
		t.Fatal("expected several lines")
	}

	var joined string
	for i, l := range lines {
		plain := ansiRegex.ReplaceAllString(l, "")
		if visibleWidth(plain) > panelWidth()+2 {
			t.Errorf("line %d escapes the panel at %d columns: %q",
				i, visibleWidth(plain), plain)
		}
		joined += strings.TrimPrefix(plain, "  "+glyphGutter+" ")
	}
	// A hard break splits the token across lines, so the URL survives only as a
	// contiguous run once the line boundaries are removed.
	if !strings.Contains(joined, url) {
		t.Errorf("splitting the URL lost or reordered it:\n%s", joined)
	}
}

// truncateANSI's guard compared COLUMNS while its loop counted RUNES, so a cell
// of double-width runes came back at nearly twice the requested width:
// truncateANSI(cjk, 15) returned 29 columns.
func TestTruncateANSIBudgetsColumnsNotRunes(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"ascii", strings.Repeat("abcdefghij", 6)},
		{"cjk", strings.Repeat("日本語のモデル名", 6)},
		{"mixed", strings.Repeat("model-日本語-name", 6)},
		{"coloured cjk", Value(strings.Repeat("日本語のモデル名", 6))},
	}
	for _, tc := range cases {
		for _, width := range []int{1, 2, 5, 15, 40} {
			got := truncateANSI(tc.text, width)
			if w := visibleWidth(got); w > width {
				t.Errorf("%s at width %d came back %d columns: %q",
					tc.name, width, w, ansiRegex.ReplaceAllString(got, ""))
			}
			// Cutting inside an escape would leave an ESC with no terminator.
			// Count well-formed sequences rather than the letter "m", which
			// also occurs in ordinary text like "model".
			if strings.Count(got, "\x1b") != len(ansiRegex.FindAllString(got, -1)) {
				t.Errorf("%s at width %d severed an escape: %q", tc.name, width, got)
			}
		}
	}
}

// The consequence in the caller: Table measures cells with runeLen and pads with
// the difference, so an over-wide truncation drove the pad negative, collapsed
// the two-space gap and shifted every column after it.
func TestTableStaysAlignedWithWideRunes(t *testing.T) {
	// Wide enough that fitTableWidths must SHAVE the CJK column — otherwise the
	// truncation path is never reached and this test proves nothing about it.
	rows := [][]string{
		{"whisper-local", strings.Repeat("日本語のモデル名", 5), "free"},
		{"groq", "whisper-large-v3-turbo", "$0.04/hr"},
	}
	lines := Table([]string{"provider", "model", "price"}, rows)
	if len(lines) != len(rows)+1 {
		t.Fatalf("expected a header and %d rows, got %d lines", len(rows), len(lines))
	}
	if !strings.Contains(lines[1], "…") {
		t.Fatal("precondition: the wide cell must actually be truncated")
	}
	for i, l := range lines {
		plain := ansiRegex.ReplaceAllString(l, "")
		if w := visibleWidth(plain); w > panelWidth()+2 {
			t.Errorf("row %d is %d columns, past the %d-column rule: %q",
				i, w, panelWidth()+2, plain)
		}
	}
	// The last column must start at the same VISIBLE column on every row — the
	// alignment the shaving exists to protect. Measured in columns, not byte
	// offsets: a CJK rune is three bytes and two columns, so a byte index would
	// report a misalignment that is not there (and miss one that is).
	col := -1
	for i, l := range lines {
		plain := ansiRegex.ReplaceAllString(l, "")
		at := visibleWidth(plain[:strings.LastIndex(plain, "  ")])
		if col == -1 {
			col = at
		} else if at != col {
			t.Errorf("row %d's final column starts at column %d, others at %d: %q",
				i, at, col, plain)
		}
	}
}

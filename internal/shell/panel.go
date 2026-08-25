// internal/shell/panel.go
//
// Purpose: one visual language for every report Helix prints.
//
// /help already had a good one — a magenta ▸ section head, a subtle │ gutter,
// a rule to close — and nothing else used it. So /blackbox status, the voice
// wizard, /tools and the setup screens each grew their own flat stack of
// color.Cyan lines: same information, no hierarchy, no containment, nothing to
// tell a heading from a value from a warning at a glance. Screenshotting the
// real terminal output made the difference obvious and embarrassing.
//
// This file is that language, extracted and made reusable. The rules it encodes:
//
//   - A report is a PANEL: a titled head, an indented body behind a gutter, and
//     a rule that closes it. The gutter is what makes a block read as one thing.
//   - Colour carries meaning, never decoration. Cyan is Helix speaking, magenta
//     is a heading, orange is a value worth reading, red is a problem, grey is
//     structure. A reader should be able to skim by colour alone.
//   - Alignment is computed from the content, never hardcoded, because a label
//     column that drifts is worse than no column at all.
//   - Width adapts to the terminal and clamps, so a wide window does not draw a
//     200-column rule and an 80-column one does not wrap.
package shell

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Panel glyphs. Box-drawing only — no emoji, no double-width runes, because
// this frames columnar output and a 2-cell glyph silently breaks alignment.
const (
	glyphSection = "▸"
	glyphGutter  = "│"
	glyphRule    = "─"
	glyphBullet  = "•"
	glyphOK      = "✔"
	glyphBad     = "✘"
	glyphWarn    = "!"
	glyphArrow   = "→"
)

// panelWidth is the rule width, adapted to the terminal and clamped.
//
// Clamped at both ends deliberately: below ~52 the columns collide, and above
// ~92 a full-width rule stops reading as a frame and starts reading as a
// horizon.
func panelWidth() int {
	w := TerminalWidth()
	switch {
	case w <= 0:
		return 72
	case w < 52:
		return 52
	case w > 92:
		return 92
	default:
		return w - 4
	}
}

// PanelTitle opens a panel: a coloured chip, the title, and a rule beneath.
//
// The chip is what makes a panel findable when scrolling back through a long
// session — a line of text is easy to miss, a block of colour is not.
func PanelTitle(title string) string {
	chip := Seg(HexPrimary, HexVoid, " "+strings.ToUpper(title)+" ")
	rule := Fg(HexSubtle, strings.Repeat(glyphRule, panelWidth()))
	return "\n  " + chip + "\n  " + rule
}

// PanelSection is a heading INSIDE a panel, for reports with more than one part.
func PanelSection(title string) string {
	return "  " + Fg(HexSecondary, glyphSection+" "+strings.ToUpper(title))
}

// PanelEnd closes a panel with the same rule it opened with.
func PanelEnd() string {
	return "  " + Fg(HexSubtle, strings.Repeat(glyphRule, panelWidth()))
}

// PanelLine renders one body line behind the gutter.
func PanelLine(text string) string {
	return "  " + Fg(HexSubtle, glyphGutter) + " " + text
}

// PanelWrap renders prose inside a panel, wrapped to the panel width with the
// gutter carried onto every continuation line.
//
// PanelLine alone is not enough for a sentence: a long one wraps at the
// terminal edge and its tail starts at column zero, visually escaping the block
// it belongs to. The endpoint-conflict note did exactly that.
//
// Args: text: prose, normally plain (colour is applied per line by the caller,
// though pre-coloured text measures correctly too).
// Returns: the wrapped lines, each already gutter-prefixed; nil for blank input.
// Complexity: O(len(text)).
func PanelWrap(text string, colour func(string) string) []string {
	if colour == nil {
		colour = func(s string) string { return s }
	}
	limit := panelWidth() - 2
	if limit < 20 {
		limit = 20
	}
	var out []string
	var line string
	flush := func() {
		if line != "" {
			out = append(out, PanelLine(colour(line)))
			line = ""
		}
	}
	for _, word := range strings.Fields(text) {
		// A word too long for any line is split rather than allowed to run past
		// the frame. A URL, an absolute path or an endpoint is the realistic
		// case, and letting one escape defeats the point of wrapping at all.
		if visibleWidth(word) > limit {
			flush()
			for _, part := range wrapANSI(word, limit) {
				out = append(out, PanelLine(colour(part)))
			}
			continue
		}
		switch {
		case line == "":
			line = word
		// Measured on VISIBLE width, not bytes. len() counts a multi-byte rune
		// once per byte, so prose containing "·", "—" or "→" — which this
		// codebase's panels are full of — wrapped up to three columns early per
		// such rune. Harmless in the sense that it never overflowed, but it
		// broke lines that had room, and it is the same class of bug as
		// measuring ANSI escapes as content.
		case visibleWidth(line)+1+visibleWidth(word) <= limit:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	flush()
	return out
}

// PanelGap is an empty gutter line — vertical breathing room that keeps the
// block visually continuous, which a bare newline does not.
func PanelGap() string {
	return "  " + Fg(HexSubtle, glyphGutter)
}

// KV renders an aligned label/value row inside a panel, wrapping the value to
// the panel width and hanging continuation lines under the value column.
//
// Wrapping is the point rather than a refinement. KV used to emit one line
// whatever its length, so a value wider than the rule wrapped at the TERMINAL
// edge instead and its tail restarted at column zero — outside the gutter,
// visually detached from the row it belongs to. That is the exact defect
// PanelWrap was written to prevent for prose, and nothing prevented it here:
// /blackbox status carried a camera message 95 columns wide in a 74-column row,
// and it read fine in source. A caller cannot reasonably be asked to keep
// every string short enough for a width it cannot see, so the primitive
// handles it.
//
// A value that already fits is emitted byte for byte as before, so every panel
// that renders correctly today is untouched.
//
// Args: label, value: already-coloured or plain; width: the label column, from
// KVWidth so every row in a block agrees.
// Returns: one or more newline-joined lines, ready for a single Println.
func KV(label, value string, width int) string {
	pad := width - runeLen(label)
	if pad < 0 {
		pad = 0
	}
	head := Fg(HexSubtle, label) + strings.Repeat(" ", pad+2)

	// The value column starts after the gutter (4 cells, per PanelLine) and the
	// label block (width + 2), inside the same limit PanelWrap uses.
	limit := panelWidth() - 2 - (width + 2)
	if limit < kvMinValueWidth {
		// A pathologically long label would otherwise leave one column per line.
		// Overflowing slightly beats a value rendered one character at a time.
		limit = kvMinValueWidth
	}

	lines := wrapANSI(value, limit)
	out := PanelLine(head + lines[0])
	indent := strings.Repeat(" ", width+2)
	for _, l := range lines[1:] {
		out += "\n" + PanelLine(indent+l)
	}
	return out
}

// kvMinValueWidth is the floor for a wrapped value column.
const kvMinValueWidth = 16

// kvCell is one printable rune plus the escape sequences that immediately
// precede it.
//
// Wrapping coloured text needs both halves separated: the escapes must travel
// with the rune they colour, but must never count toward a width or be severed
// by a line break. Pairing them makes wrapping pure index arithmetic over
// visible cells, with the colour reassembled afterwards.
type kvCell struct {
	pre string // escape sequences sitting in front of this rune
	r   rune
	w   int // visible cells this rune occupies (0, 1 or 2)
}

// splitCells decomposes a coloured string into cells plus any trailing escapes
// (usually the final reset, which belongs to no rune).
func splitCells(s string) ([]kvCell, string) {
	rs := []rune(s)
	out := make([]kvCell, 0, len(rs))
	var pre strings.Builder
	for i := 0; i < len(rs); i++ {
		if rs[i] == 0x1b {
			start := i
			for i < len(rs) && rs[i] != 'm' {
				i++
			}
			end := i + 1
			if end > len(rs) {
				end = len(rs)
			}
			pre.WriteString(string(rs[start:end]))
			continue
		}
		out = append(out, kvCell{pre: pre.String(), r: rs[i], w: runewidth.RuneWidth(rs[i])})
		pre.Reset()
	}
	return out, pre.String()
}

// sgrAfter reports which SGR sequence is still in effect after `esc`, given the
// one in effect before it. A reset clears; anything else replaces.
func sgrAfter(prev, esc string) string {
	rs := []rune(esc)
	for i := 0; i < len(rs); i++ {
		if rs[i] != 0x1b {
			continue
		}
		start := i
		for i < len(rs) && rs[i] != 'm' {
			i++
		}
		end := i + 1
		if end > len(rs) {
			end = len(rs)
		}
		if seq := string(rs[start:end]); seq == ansiReset {
			prev = ""
		} else {
			prev = seq
		}
	}
	return prev
}

// wrapANSI breaks an already-coloured string into lines of at most `width`
// visible columns, preferring word boundaries.
//
// Colour is carried across breaks: the sequence in effect at the end of a line
// is closed there and reopened at the start of the next. Without that, a
// wrapped value renders its first line coloured and the remainder plain, and a
// line that ended mid-colour would bleed into the gutter glyph beneath it.
//
// A string that already fits is returned untouched, byte for byte, so every
// panel that fits today renders exactly as it does today.
func wrapANSI(s string, width int) []string {
	if width <= 0 || visibleWidth(s) <= width {
		return []string{s}
	}
	cells, tail := splitCells(s)

	// Greedy fill. Each iteration consumes at least one cell, so this
	// terminates even when width is smaller than a single wide rune.
	var groups [][]kvCell
	for i := 0; i < len(cells); {
		if len(groups) > 0 {
			// A continuation line never starts with the padding that separated
			// two words on the line above.
			for i < len(cells) && cells[i].r == ' ' {
				i++
			}
			if i >= len(cells) {
				break
			}
		}
		end, w, lastSpace := i, 0, -1
		for end < len(cells) && w+cells[end].w <= width {
			if cells[end].r == ' ' {
				lastSpace = end
			}
			w += cells[end].w
			end++
		}
		if end < len(cells) && lastSpace > i {
			end = lastSpace // break at the word boundary rather than mid-word
		}
		if end == i {
			end = i + 1 // width narrower than one cell: make progress anyway
		}
		groups = append(groups, cells[i:end])
		i = end
	}

	out := make([]string, 0, len(groups))
	open := "" // colour in effect when the current line starts
	for gi, g := range groups {
		var b strings.Builder
		b.WriteString(open)
		state := open
		for _, c := range g {
			if c.pre != "" {
				b.WriteString(c.pre)
				state = sgrAfter(state, c.pre)
			}
			b.WriteRune(c.r)
		}
		if gi == len(groups)-1 && tail != "" {
			b.WriteString(tail)
			state = sgrAfter(state, tail)
		}
		if state != "" {
			b.WriteString(ansiReset)
		}
		out = append(out, b.String())
		open = state
	}
	return out
}

// KVWidth is the widest label in a set, so a caller never hardcodes a column.
func KVWidth(labels ...string) int {
	w := 0
	for _, l := range labels {
		if n := runeLen(l); n > w {
			w = n
		}
	}
	return w
}

// Status renders a state word in the colour that state deserves.
//
// Centralised so "ready" is the same green everywhere and, more importantly, so
// nothing can print a healthy-looking word for an unhealthy state — the class
// of bug that had /eyes reporting "ready" on a machine with no camera pipeline.
type State int

const (
	StateGood State = iota
	StateWarn
	StateBad
	StateIdle
)

// Badge renders a state word with its glyph.
func Badge(s State, text string) string {
	switch s {
	case StateGood:
		return Fg(HexPrimary, glyphOK+" ") + Fg(HexText, text)
	case StateWarn:
		return Fg(HexTertiary, glyphWarn+" ") + Fg(HexTertiary, text)
	case StateBad:
		return Fg(HexRectifier, glyphBad+" ") + Fg(HexRectifier, text)
	default:
		return Fg(HexSubtle, glyphBullet+" ") + Fg(HexSubtle, text)
	}
}

// PadVisible pads an already-coloured string to a visible width.
//
// fmt's %-9s counts the ANSI escape bytes, so padding a coloured cell that way
// pads to nothing at all — the alignment bug this whole file exists to stop
// re-inventing.
func PadVisible(s string, width int) string {
	if pad := width - runeLen(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// Value highlights a value worth reading — a model name, a port, a path.
func Value(s string) string { return Fg(HexTertiary, s) }

// Muted renders secondary detail that should not compete with the value.
func Muted(s string) string { return Fg(HexSubtle, s) }

// Arrow joins the links of a chain (a provider failover chain, a pipeline).
func Arrow() string { return Fg(HexSubtle, " "+glyphArrow+" ") }

// Table renders aligned columns behind the panel gutter.
//
// Widths come from the content, so a long provider name widens its column
// instead of colliding with the next one. Cells may already be coloured —
// widths are measured on the visible text, which is why runeLen strips ANSI.
//
// Args: headers: column titles; rows: cells, ragged rows are padded.
// Returns: the rendered lines, header first.
// Complexity: O(cells).
func Table(headers []string, rows [][]string) []string {
	if len(headers) == 0 {
		return nil
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = runeLen(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && runeLen(c) > widths[i] {
				widths[i] = runeLen(c)
			}
		}
	}

	// Fit the grid to the panel. A table wider than its frame wraps at the
	// terminal edge and the tail restarts at column zero, destroying the
	// alignment the table exists for — the nine-column pricing table pushed its
	// last cell onto a line of its own, outside the gutter.
	//
	// The widest column is shaved first, repeatedly, because that is the one
	// carrying a long model name or endpoint; narrow columns are structural and
	// shrinking them would cost more meaning per character.
	fitTableWidths(widths, panelWidth()-2)

	render := func(cells []string, colour func(string) string) string {
		var b strings.Builder
		for i := range headers {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			cell = truncateANSI(cell, widths[i])
			pad := widths[i] - runeLen(cell)
			if pad < 0 {
				pad = 0
			}
			b.WriteString(colour(cell))
			if i < len(headers)-1 {
				b.WriteString(strings.Repeat(" ", pad+2))
			}
		}
		return b.String()
	}

	out := []string{
		PanelLine(render(headers, func(s string) string { return Fg(HexSubtle, strings.ToUpper(s)) })),
	}
	for _, r := range rows {
		out = append(out, PanelLine(render(r, func(s string) string { return s })))
	}
	return out
}

// MenuItem is one numbered choice in a wizard.
type MenuItem struct {
	Label string // what it is
	Note  string // why you would pick it, or what it costs
	Tag   string // a short marker: "recommended", "installed", "needs a key"
	Good  bool   // render the tag as an endorsement rather than a caution
}

// Menu renders a numbered choice list inside a panel.
//
// Numbers are right-aligned and dim, labels are the value colour, and the
// reason to pick one sits in the muted column beside it — because a wizard's
// job is to make a decision easy, and a bare list of names makes the reader
// carry the differences in their head. `1) OpenAI` told you nothing that
// `openai — key required · cloud` does not.
//
// Args: items in presentation order.
// Returns: the rendered lines.
// Complexity: O(items).
func Menu(items []MenuItem) []string {
	labelWidth := 0
	for _, it := range items {
		if n := runeLen(it.Label); n > labelWidth {
			labelWidth = n
		}
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		num := Fg(HexSubtle, fmt.Sprintf("%2d", i+1)) + Fg(HexSubtle, ")")
		line := num + " " + PadVisible(Fg(HexTertiary, it.Label), labelWidth+2)
		if it.Note != "" {
			line += Muted(it.Note)
		}
		if it.Tag != "" {
			colour := HexSubtle
			if it.Good {
				colour = HexPrimary
			}
			line += "  " + Fg(colour, "["+it.Tag+"]")
		}
		out = append(out, PanelLine(line))
	}
	return out
}

// Prompt renders the question line that follows a menu, so every wizard asks
// in the same voice and in the same place.
func Prompt(question, def string) string {
	q := Fg(HexPrimary, question)
	if def != "" {
		q += Muted("  [" + def + "]")
	}
	return "  " + Fg(HexSecondary, glyphSection) + " " + q
}

// fitTableWidths shrinks columns in place until the row fits the limit.
//
// Args: widths: per-column visible widths, modified in place; limit: the space
// available including the two-space gaps between columns.
// Complexity: O(columns × overflow), bounded by the loop guard.
func fitTableWidths(widths []int, limit int) {
	if len(widths) == 0 || limit <= 0 {
		return
	}
	const minColumn = 4

	total := func() int {
		sum := 2 * (len(widths) - 1) // the gaps
		for _, w := range widths {
			sum += w
		}
		return sum
	}
	// Bounded: every iteration removes a cell of width, and the floor stops it.
	for guard := 0; total() > limit && guard < 4096; guard++ {
		widest, at := 0, -1
		for i, w := range widths {
			if w > widest && w > minColumn {
				widest, at = w, i
			}
		}
		if at < 0 {
			return // everything is at the floor; nothing more to give
		}
		widths[at]--
	}
}

// truncateANSI cuts a possibly-coloured string to a visible width, in COLUMNS.
//
// Escape sequences are copied verbatim and never counted, so colour survives
// the cut and the reset at the end of the original still lands — naive slicing
// would either count escape bytes as content or sever a sequence mid-way and
// bleed the colour into the rest of the line.
//
// The budget is columns rather than runes, which is not the same thing and used
// not to be honoured: the guard below compares columns while the loop used to
// count runes, so a CJK cell — every rune two columns wide — came back at up to
// twice the requested width. `truncateANSI(cjk, 15)` returned 29 columns. Table
// is entirely column-based (it measures cells with runeLen and pads with the
// difference), so an over-wide cell drove its pad negative, collapsed the gap to
// nothing and shifted every column after it — destroying exactly the alignment
// Table shaves columns to preserve.
//
// A wide rune that does not fit in what remains is dropped rather than half
// printed, so the result may come back one column under budget. Under is safe;
// over is the bug.
func truncateANSI(s string, width int) string {
	if width <= 0 || runeLen(s) <= width {
		return s
	}
	// The ellipsis occupies part of the budget; measure it rather than assuming
	// one column, since that is the assumption this function is being fixed for.
	const ellipsis = '…'
	budget := width - runewidth.RuneWidth(ellipsis)
	if budget < 0 {
		budget = 0
	}

	var b strings.Builder
	seen := 0
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == 0x1b {
			for i < len(runes) {
				b.WriteRune(runes[i])
				if runes[i] == 'm' {
					break
				}
				i++
			}
			continue
		}
		w := runewidth.RuneWidth(runes[i])
		if seen+w > budget {
			break
		}
		b.WriteRune(runes[i])
		seen += w
	}
	b.WriteRune(ellipsis)
	b.WriteString(ansiReset)
	return b.String()
}

// Hint renders an actionable suggestion — the thing to type next.
//
// Visually distinct from a warning on purpose: a warning says something is
// wrong, a hint says what to do about it, and running them together in the same
// yellow is why long reports stop being read.
func Hint(text string) string {
	return PanelLine(Fg(HexSubtle, glyphArrow+" ") + Fg(HexPrimary, text))
}

// runeLen is the visible width of a string, ignoring ANSI colour. Cells are
// often already coloured, and measuring the escape codes would silently skew
// every column to the right.
func runeLen(s string) int { return visibleWidth(s) }

// PrintPanel is the convenience form: title, body lines, closing rule.
func PrintPanel(title string, lines ...string) {
	fmt.Println(PanelTitle(title))
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Println(PanelEnd())
}

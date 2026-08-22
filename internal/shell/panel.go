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
// Args: text: plain prose (colour is applied to the whole line by the caller).
// Returns: the wrapped lines, each already gutter-prefixed.
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
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= limit:
			line += " " + word
		default:
			out = append(out, PanelLine(colour(line)))
			line = word
		}
	}
	if line != "" {
		out = append(out, PanelLine(colour(line)))
	}
	return out
}

// PanelGap is an empty gutter line — vertical breathing room that keeps the
// block visually continuous, which a bare newline does not.
func PanelGap() string {
	return "  " + Fg(HexSubtle, glyphGutter)
}

// KV renders an aligned label/value row inside a panel.
//
// Args: label, value: already-coloured or plain; width: the label column,
// from KVWidth so every row in a block agrees.
func KV(label, value string, width int) string {
	pad := width - runeLen(label)
	if pad < 0 {
		pad = 0
	}
	return PanelLine(Fg(HexSubtle, label) + strings.Repeat(" ", pad+2) + value)
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

// truncateANSI cuts a possibly-coloured string to a visible width.
//
// Escape sequences are copied verbatim and never counted, so colour survives
// the cut and the reset at the end of the original still lands — naive slicing
// would either count escape bytes as content or sever a sequence mid-way and
// bleed the colour into the rest of the line.
func truncateANSI(s string, width int) string {
	if width <= 0 || runeLen(s) <= width {
		return s
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
		if seen >= width-1 {
			break
		}
		b.WriteRune(runes[i])
		seen++
	}
	b.WriteString("…")
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

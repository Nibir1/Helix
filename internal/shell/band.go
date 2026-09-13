// internal/shell/band.go
// Purpose: a conversation turn as a labelled band — the "instrument readout"
// layout.
//
// WHAT IT REPLACES AND WHY. A turn used to be a bare prefix and a line of
// prose: `[NEURAL_NET] → ...` running to whatever width the terminal happened
// to be, under panels that stop at 92. The result was reported as "everything
// is just stuck on the left and looks stale" — and it was not really about the
// left. It was that nothing shared a right edge, so the screen had no measure
// and every element looked independently placed.
//
// A band gives a turn three things a prefix cannot: a rule that says WHO is
// speaking, a rail that holds the prose to one measure however long it runs,
// and the model that produced it, in the rule rather than trailing the text.
//
// STREAMING IS THE HARD PART, and it is the reason this is a writer rather than
// a formatter. A reply arrives token by token, so the wrap cannot be computed
// over a finished string: BandWriter tracks the current column, buffers the
// word in flight, and breaks at the last whitespace before the measure. Nothing
// is ever emitted that the caller has not produced, so a response that stops
// mid-sentence leaves a correct partial band rather than a hanging rule.
package shell

import (
	"fmt"
	"strings"
)

// Band glyphs. Heavy box-drawing, distinct from the light glyphs panels use, so
// a conversation band and a status panel are not mistaken for each other at a
// glance. Single-width only, for the reason the panel glyph block states.
const (
	glyphBandCorner = "┏"
	glyphBandRule   = "━"
	glyphBandRail   = "┃"
	glyphBandClose  = "┗"
)

// bandIndent is the left margin, matching PanelLine's gutter.
const bandIndent = "  "

// bandRail is what every wrapped line of a band opens with.
func bandRail() string { return bandIndent + Fg(HexSubtle, glyphBandRail) + " " }

// bandContentWidth is how much prose fits on one rail line.
//
// DERIVED, not guessed: the whole line must fit the same budget a panel line
// does, so the content is that budget minus the rail it sits behind. The first
// version subtracted a hardcoded 2 for a rail that is 4 columns wide, and every
// full line came out one column over — which is how a shared right edge stops
// being shared.
func bandContentWidth() int {
	w := panelWidth() + 2 - visibleWidth(bandRail())
	if w < 24 {
		w = 24
	}
	return w
}

// BandHeader renders the opening rule of a turn.
//
// The label is left, the meta (the model that produced the turn) is right, and
// the rule stretches between them — so the eye can find either without reading
// the other, and a turn always states which model is responsible for it. That
// last part is not decoration: the session that prompted this layout included a
// user asking "who is processing that text?", and the answer had been nowhere
// on screen.
func BandHeader(label, meta, colour string) string {
	label = strings.TrimSpace(label)
	meta = strings.TrimSpace(meta)

	left := Fg(HexSubtle, glyphBandCorner+glyphBandRule) + " " + Fg(colour, label) + " "
	right := ""
	if meta != "" {
		right = " " + Muted(meta) + " " + Fg(HexSubtle, glyphBandRule)
	}
	fill := panelWidth() - visibleWidth(left) - visibleWidth(right) - len(bandIndent)
	if fill < 2 {
		fill = 2
	}
	return bandIndent + left + Fg(HexSubtle, strings.Repeat(glyphBandRule, fill)) + right
}

// BandClose renders the closing rule. Used at the end of a run of bands rather
// than after every one: a rule under each turn doubles the chrome and makes a
// conversation read as a stack of forms.
func BandClose() string {
	return bandIndent + Fg(HexSubtle, glyphBandClose+strings.Repeat(glyphBandRule, panelWidth()-len(bandIndent)))
}

// BandWriter streams prose into a band, wrapping it to one measure.
//
// Not safe for concurrent use: a turn has one producer.
type BandWriter struct {
	// width is unused as a cache and kept only for tests to pin a measure.
	//
	// IT USED TO BE CAPTURED ONCE, on the argument that a resize mid-reply
	// would otherwise give one band two right edges. That argument had the
	// trade backwards: the lines already printed cannot be reflowed whatever we
	// do — they are the terminal's scrollback now — so caching does not buy a
	// straight edge, it only guarantees that every line AFTER a shrink is wider
	// than the terminal. Which is the marching-lines bug this layout was
	// written to fix, reintroduced one level up.
	//
	// So the measure is re-read per line: a resize leaves a step in the right
	// edge, and never a line the terminal has to wrap.
	//
	// pinned is a fixed measure for tests, which have no terminal to read.
	pinned int

	col     int             // columns used on the current line
	word    strings.Builder // the word in flight, not yet committed
	wordW   int             // its visible width
	space   bool            // a separator is owed before the next word
	started bool            // has anything been emitted
	out     func(string)    // where lines go; indirected for tests
}

// NewBandWriter starts a band body.
func NewBandWriter() *BandWriter {
	return &BandWriter{out: func(s string) { fmt.Print(s) }}
}

// WriteString feeds one streamed fragment.
func (b *BandWriter) WriteString(text string) {
	for _, r := range text {
		switch r {
		case '\n':
			b.flushWord()
			b.newline()
		case ' ', '\t':
			b.flushWord()
			// The separator is OWED, not written. Emitting it here put a space
			// on a line that was already full, which the wrap then could not
			// take back — every full line came out one column over. Deferring
			// it means a space is only ever written between two words that are
			// both on the same line, and trailing spaces cannot exist.
			if b.col > 0 {
				b.space = true
			}
		default:
			b.word.WriteRune(r)
			b.wordW += runeCells(r)
		}
	}
}

// flushWord commits the buffered word, wrapping first if it will not fit.
func (b *BandWriter) flushWord() {
	if b.wordW == 0 {
		return
	}
	sep := 0
	if b.space && b.col > 0 {
		sep = 1
	}
	if b.col > 0 && b.col+sep+b.wordW > b.measure() {
		b.newline() // the owed separator dies with the line break
	} else if sep == 1 {
		b.emit(" ")
		b.col++
	}
	b.space = false
	b.emit(b.word.String())
	b.col += b.wordW
	b.word.Reset()
	b.wordW = 0
}

// measure is the prose width for the line being started RIGHT NOW.
//
// A pinned width (tests) wins; otherwise the terminal is re-read, so a window
// resized mid-reply changes the wrap from the next line onward.
func (b *BandWriter) measure() int {
	if b.pinned > 0 {
		return b.pinned
	}
	return bandContentWidth()
}

// newline ends the current rail line and opens the next.
func (b *BandWriter) newline() {
	b.emit("\n")
	b.emit(bandRail())
	b.col = 0
	b.space = false
}

// emit writes, opening the band's first rail lazily.
//
// Lazy so a turn that produces NOTHING leaves no orphaned rail on screen — the
// same reason AIStreamWriter defers its prefix, and the same failure it was
// avoiding.
func (b *BandWriter) emit(s string) {
	if !b.started {
		b.out(bandRail())
		b.started = true
	}
	b.out(s)
}

// Started reports whether any prose was rendered.
func (b *BandWriter) Started() bool { return b.started }

// Close finishes the band body.
func (b *BandWriter) Close() {
	b.flushWord()
	if b.started {
		b.out("\n")
	}
}

// BandLines renders a finished string as band body lines, for callers that are
// not streaming.
func BandLines(text string) []string {
	// Collect everything the writer emits and split once, rather than trying
	// to reassemble lines from the fragments as they arrive. The first version
	// did the latter and got it wrong three different ways — the writer streams
	// arbitrary pieces and only IT knows where a line ends.
	var sb strings.Builder
	w := &BandWriter{out: func(s string) { sb.WriteString(s) }}
	w.WriteString(text)
	w.Close()

	body := strings.TrimSuffix(sb.String(), "\n")
	if body == "" {
		return nil
	}
	return strings.Split(body, "\n")
}

// runeCells is the column width of one rune.
func runeCells(r rune) int { return visibleWidth(string(r)) }

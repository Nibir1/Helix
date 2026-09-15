// internal/shell/width_test.go
// Purpose: a failed measurement must not narrow the next hundred lines.
//
// BandWriter re-measures per line so a window resized mid-reply reflows, which
// means a long answer probes the terminal a hundred times. One failed probe
// returned 0, panelWidth fell to its 52-column floor, and the REST of that
// reply wrapped at ~46 columns on a 200-column terminal — with the band header
// still spanning the full width, because that was measured once, when it
// worked. On screen it reads as the reply having been capped.
package shell

import (
	"strings"
	"testing"
)

func TestAFailedProbeFallsBackToTheLastRealWidth(t *testing.T) {
	prev := lastGoodWidth.Load()
	t.Cleanup(func() { lastGoodWidth.Store(prev) })

	lastGoodWidth.Store(200)
	// TerminalWidth cannot be driven from a test — there is no terminal — so
	// the fallback is exercised directly. Under `go test` every probe fails,
	// which is precisely the condition being modelled.
	if got := TerminalWidth(); got != 200 {
		t.Fatalf("TerminalWidth() = %d after a failed probe, want the last real "+
			"width 200 — a blip mid-reply narrows everything after it", got)
	}

	// And with nothing ever measured, it still reports honestly rather than
	// inventing a width.
	lastGoodWidth.Store(0)
	if got := TerminalWidth(); got != 0 {
		t.Errorf("TerminalWidth() = %d with nothing ever measured, want 0", got)
	}
}

// The floor is what a zero turns into, and it is 46 columns of content on a
// terminal that may be four times that.
func TestADegenerateWidthNarrowsTheBandDramatically(t *testing.T) {
	wide := panelWidthFor(200)
	floored := panelWidthFor(0)
	if floored >= wide {
		t.Fatal("the floor is not narrower than a real width; this test models nothing")
	}
	if wide-floored < 100 {
		t.Errorf("a failed probe costs only %d columns; the fallback would not be "+
			"worth having", wide-floored)
	}
}

// The band header marks where a turn starts and says who is answering. It does
// NOT stretch to the panel edge: one turn looked deliberate, but a long
// conversation became a stack of full-width horizontal bars every three or four
// lines, and on a wide terminal those are the loudest thing on screen.
func TestBandHeaderDoesNotSpanTheWholeWidth(t *testing.T) {
	prev := lastGoodWidth.Load()
	t.Cleanup(func() { lastGoodWidth.Store(prev) })
	lastGoodWidth.Store(200)

	h := Plain(BandHeader("HELIX", "deepseek / deepseek-flash", HexPrimary))
	if got := visibleWidth(h); got > 60 {
		t.Errorf("the header is %d columns wide on a 200-column terminal — a long "+
			"conversation is a stack of full-width rules:\n%q", got, h)
	}
	// It still has to do its job.
	if !strings.Contains(h, "HELIX") {
		t.Errorf("the header does not name the speaker: %q", h)
	}
	if !strings.Contains(h, "deepseek / deepseek-flash") {
		t.Errorf("the header does not name the model that answered: %q", h)
	}
	if !strings.HasPrefix(h, bandIndent+glyphBandCorner) {
		t.Errorf("the header lost its corner, so a turn no longer has a visible start: %q", h)
	}
}

// A header with no model still reads as a header rather than as a stray word.
func TestBandHeaderWithoutMeta(t *testing.T) {
	h := Plain(BandHeader("HELIX", "", HexPrimary))
	if !strings.Contains(h, "HELIX") || !strings.HasPrefix(h, bandIndent+glyphBandCorner) {
		t.Errorf("a header with no model is malformed: %q", h)
	}
	if strings.HasSuffix(strings.TrimSpace(h), glyphBandRule) {
		t.Errorf("a header with no model ends in a dangling rule: %q", h)
	}
}

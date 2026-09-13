// internal/shell/band_test.go
// Purpose: a band must hold ONE measure however the prose arrives.
//
// The whole point of the layout is a shared right edge, so the assertions are
// about width — and width is exactly what this package has been wrong about
// three times (PanelWrap counting bytes, truncateANSI counting runes, and the
// live caption wrapping off the bottom of the screen).
package shell

import (
	"strings"
	"testing"
)

// capture collects what a BandWriter emits.
func capture() (*BandWriter, *strings.Builder) {
	var sb strings.Builder
	w := &BandWriter{width: bandContentWidth(), out: func(s string) { sb.WriteString(s) }}
	return w, &sb
}

// Streamed a rune at a time or in one lump, the wrap must be identical. A reply
// arrives in provider-shaped chunks and must not render differently because of
// where the token boundaries fell.
func TestChunkBoundariesDoNotChangeTheWrap(t *testing.T) {
	const prose = "Yes, that is the right shape. Spawn it in a sandbox, run tests against it, " +
		"check exit codes and output, then promote it only if it passes the manifest."

	whole, a := capture()
	whole.WriteString(prose)
	whole.Close()

	runes, b := capture()
	for _, r := range prose {
		runes.WriteString(string(r))
	}
	runes.Close()

	lumps, c := capture()
	for i := 0; i < len(prose); i += 7 {
		lumps.WriteString(prose[i:min(i+7, len(prose))])
	}
	lumps.Close()

	if a.String() != b.String() {
		t.Errorf("rune-at-a-time differs from whole:\n%q\n%q", a.String(), b.String())
	}
	if a.String() != c.String() {
		t.Errorf("7-byte chunks differ from whole:\n%q\n%q", a.String(), c.String())
	}
}

// No line may exceed the measure. This is the assertion the layout exists for.
func TestNoBandLineExceedsTheMeasure(t *testing.T) {
	long := strings.Repeat("so if I want to give you the capabilities to create functionalities ", 8)
	limit := panelWidth() + 2
	for _, line := range BandLines(long) {
		if w := visibleWidth(line); w > limit {
			t.Errorf("band line is %d columns, limit %d: %q", w, limit, Plain(line))
		}
	}
}

// Every body line must carry the rail, or the band has a hole in it.
func TestEveryBandLineCarriesTheRail(t *testing.T) {
	long := strings.Repeat("alpha bravo charlie delta echo foxtrot golf hotel ", 6)
	lines := BandLines(long)
	if len(lines) < 3 {
		t.Fatalf("expected several wrapped lines, got %d", len(lines))
	}
	for i, line := range lines {
		if !strings.Contains(Plain(line), glyphBandRail) {
			t.Errorf("line %d has no rail: %q", i, Plain(line))
		}
	}
}

// A turn that produces nothing must leave nothing. The prefix version of this
// bug is documented on AIStreamWriter: an orphaned "[NEURAL_NET] →" with no
// reply under it.
func TestAnEmptyTurnLeavesNoOrphanedRail(t *testing.T) {
	w, sb := capture()
	w.Close()
	if sb.String() != "" {
		t.Errorf("an empty band emitted %q", sb.String())
	}
	if w.Started() {
		t.Error("an empty band reports as started")
	}
}

// The header must state who is speaking AND which model produced the turn —
// the question that prompted this layout was "who is processing that text?".
func TestTheHeaderNamesBothTheSpeakerAndTheModel(t *testing.T) {
	got := Plain(BandHeader("HELIX", "deepseek/deepseek-flash", HexPrimary))
	if !strings.Contains(got, "HELIX") {
		t.Errorf("header omits the speaker: %q", got)
	}
	if !strings.Contains(got, "deepseek/deepseek-flash") {
		t.Errorf("header omits the model: %q", got)
	}
	if w := visibleWidth(BandHeader("HELIX", "deepseek/deepseek-flash", HexPrimary)); w > panelWidth()+2 {
		t.Errorf("header is %d columns, panel is %d", w, panelWidth())
	}
}

// A header with no model still renders a clean rule.
func TestAHeaderWithoutAModelStillFillsTheRule(t *testing.T) {
	got := BandHeader("OPERATOR", "", HexSecondary)
	if w := visibleWidth(got); w > panelWidth()+2 {
		t.Errorf("header is %d columns", w)
	}
	if !strings.Contains(Plain(got), glyphBandRule) {
		t.Errorf("header has no rule: %q", Plain(got))
	}
}

// An explicit newline in the prose must break the line, not be swallowed.
func TestAuthoredNewlinesSurvive(t *testing.T) {
	lines := BandLines("first line\nsecond line")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(Plain(lines[0]), "first line") ||
		!strings.Contains(Plain(lines[1]), "second line") {
		t.Errorf("authored break was mangled: %q", lines)
	}
}

// A single word longer than the measure must not loop forever or overflow
// unboundedly — the pathological case every wrapper gets wrong once.
func TestAWordLongerThanTheMeasureTerminates(t *testing.T) {
	word := strings.Repeat("x", panelWidth()*3)
	lines := BandLines(word)
	if len(lines) == 0 {
		t.Fatal("a long word produced no output")
	}
	// It may overflow its own line — breaking inside a token would corrupt a
	// path or a hash — but it must not produce unbounded lines.
	if len(lines) > 3 {
		t.Errorf("a single word produced %d lines", len(lines))
	}
}

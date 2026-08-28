// internal/shell/contrast_test.go
// Purpose: text Helix prints must be readable on a dark terminal.
//
// This is pinned with numbers because it failed silently for a long time and
// nobody could point at a rule. HexSubtle was #444444 — 1.44:1 against an
// ordinary dark background, where WCAG's floor for body text is 4.5:1 — and it
// was used for BOTH panel rules, which should recede, and /about's prose, which
// a person is expected to read. At one value the readable half loses, and the
// philosophy section rendered in text that was very nearly invisible.
//
// The palette is split by ROLE now: HexSubtle is chrome (rules, gutters, the
// ghost-text suggestion), HexMuted is secondary text. This file holds each to
// the standard its role requires.
package shell

import (
	"fmt"
	"math"
	"testing"
)

// darkBackground is the reference. #282C34 is a very common dark theme and the
// LIGHTER of the plausible backgrounds, so measuring against it is the worst
// case rather than the flattering one — pure black scores every colour higher.
const darkBackground = "#282C34"

// relativeLuminance implements WCAG 2.x.
func relativeLuminance(hex string) float64 {
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		panic("bad hex: " + hex)
	}
	lin := func(v int) float64 {
		c := float64(v) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrastRatio is the WCAG ratio between two colours, 1.0 to 21.0.
func contrastRatio(a, b string) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	hi, lo := math.Max(la, lb), math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

// Anything a person is expected to READ must clear WCAG AA for body text.
func TestTextColoursAreReadable(t *testing.T) {
	for _, c := range []struct {
		hex, name string
	}{
		{HexText, "HexText (primary prose)"},
		{HexMuted, "HexMuted (secondary prose, labels, table headers)"},
		{HexAmber, "HexAmber (values)"},
		{HexTertiary, "HexTertiary (warnings)"},
		{HexPrimary, "HexPrimary (hints, good badges)"},
	} {
		if got := contrastRatio(c.hex, darkBackground); got < 4.5 {
			t.Errorf("%s = %s is %.2f:1 against %s; body text needs 4.5:1",
				c.name, c.hex, got, darkBackground)
		}
	}
}

// Chrome may recede — that is its job — but a rule nobody can see is not a
// frame. It must clear the 3:1 that WCAG asks of non-text UI boundaries.
func TestChromeIsVisibleWithoutCompeting(t *testing.T) {
	got := contrastRatio(HexSubtle, darkBackground)
	if got < 2.0 {
		t.Errorf("HexSubtle = %s is %.2f:1; panel rules would be invisible", HexSubtle, got)
	}
	// And it must stay BELOW the text tone, or the frame competes with what it
	// frames — which is the failure in the other direction.
	if got >= contrastRatio(HexMuted, darkBackground) {
		t.Errorf("chrome (%.2f:1) must recede behind secondary text (%.2f:1)",
			got, contrastRatio(HexMuted, darkBackground))
	}
}

// The old value, kept as a named regression. If any text colour ever drifts
// back to this neighbourhood, this is the number that explains why it is wrong.
func TestTheOldSubtleGreyWouldStillFail(t *testing.T) {
	const wasUnreadable = "#444444"
	if got := contrastRatio(wasUnreadable, darkBackground); got >= 4.5 {
		t.Fatalf("premise changed: %s now measures %.2f:1", wasUnreadable, got)
	}
	if HexMuted == wasUnreadable || HexText == wasUnreadable {
		t.Error("a text colour was set back to the grey that started this")
	}
}

// State badges must stay distinguishable BY COLOUR, not only by glyph — a
// reader scanning a panel sees the colour first.
func TestBadgeColoursAreDistinctEnough(t *testing.T) {
	for _, pair := range [][2]string{
		{HexPrimary, HexTertiary},
		{HexPrimary, HexRectifier},
		{HexTertiary, HexRectifier},
	} {
		if pair[0] == pair[1] {
			t.Errorf("two badge states share the colour %s", pair[0])
		}
	}
}

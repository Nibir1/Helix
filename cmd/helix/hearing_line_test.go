// cmd/helix/hearing_line_test.go
// Purpose: the caption must never be wider than the terminal.
//
// Reported with a screenshot of forty near-identical lines marching down the
// screen: once the partial outgrew a row the terminal wrapped it, and `\r` +
// `\x1b[2K` cleared only the last of those rows. Every redraw left its
// predecessors behind. Half-duplex hid it because a clip is bounded; full
// duplex has no bound.
package main

import (
	"os"
	"strings"
	"testing"
)

// The whole defect in one assertion: whatever is said, one row.
func TestTheCaptionNeverExceedsOneRow(t *testing.T) {
	long := strings.Repeat("so if I want to give you the capabilities to create functionalities ", 12)
	// Every width a real terminal plausibly has, including the awkward ones.
	for _, width := range []int{40, 60, 80, 100, 125, 200} {
		got := hearingLineAt(long, width)
		if strings.Contains(got, "\n") {
			t.Fatalf("width %d: the caption contains a newline", width)
		}
		if n := len([]rune(got)); n >= width {
			t.Errorf("width %d: caption is %d columns; the terminal wraps it and every "+
				"redraw leaves the previous rows on screen", width, n)
		}
	}
}

// The TAIL is what matters. While a sentence is still arriving, the words just
// spoken are the useful ones — pinning the opening hides everything new.
func TestTheCaptionKeepsTheMostRecentWords(t *testing.T) {
	got := hearingLineAt("the opening words nobody needs any more and then FINALLY the newest part", 60)
	if !strings.Contains(got, "the newest part") {
		t.Errorf("the caption dropped the most recent words: %q", got)
	}
	if strings.Contains(got, "the opening words") {
		t.Errorf("the caption pinned the opening instead of following the speech: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a truncated caption does not say it was truncated: %q", got)
	}
}

// Short speech must be untouched — no ellipsis, no clipping.
func TestAShortCaptionIsLeftAlone(t *testing.T) {
	const said = "what is in this directory"
	got := hearingLineAt(said, 120)
	if got != hearingPrefix+said {
		t.Errorf("hearingLine(%q) = %q", said, got)
	}
}

// A terminal too narrow for any caption still says the microphone is live.
func TestAVeryNarrowTerminalStillShowsTheLabel(t *testing.T) {
	got := hearingLineAt("a long sentence that cannot possibly fit", 12)
	if !strings.HasPrefix(got, "[hearing]") {
		t.Errorf("a narrow terminal loses the label entirely: %q", got)
	}
	if len([]rune(got)) > 12 {
		t.Errorf("caption is %d columns in a 12-column terminal: %q", len([]rune(got)), got)
	}
}

// Both capture paths must use it. The duplex one is where the bug was seen; the
// streaming one has the same latent defect and shorter utterances.
func TestBothCapturePathsPaintThroughTheClampedHelper(t *testing.T) {
	for _, file := range []string{"duplex.go", "voice_mode.go"} {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		body := string(src)
		if strings.Contains(body, `\r\x1b[2K[hearing] %s`) || strings.Contains(body, `"\r[hearing] %s"`) {
			t.Errorf("%s still prints the caption unclamped; a long sentence wraps and "+
				"repeats down the screen", file)
		}
		if !strings.Contains(body, "paintHearing(") {
			t.Errorf("%s does not paint through the clamped helper", file)
		}
	}
}

// No terminal to measure — a pipe, a test binary — must not clamp to nothing.
func TestAnUnmeasurableTerminalEmitsTheTextUnclamped(t *testing.T) {
	const said = "a sentence of some length that would otherwise be cut"
	if got := hearingLineAt(said, 0); got != hearingPrefix+said {
		t.Errorf("hearingLineAt(_, 0) = %q; with no width there is no wrapping to prevent", got)
	}
}

// cmd/helix/hearing_line.go
// Purpose: paint the live transcript on ONE terminal row, whatever the speaker
// does.
//
// THE BUG THIS EXISTS FOR, reported from a real session with a screenshot of
// forty near-identical lines marching down the screen. Both capture paths drew
// the partial with
//
//	fmt.Printf("\r\x1b[2K[hearing] %s", text)
//
// which redraws one line in place — right up to the moment the text is wider
// than the terminal. Then the terminal WRAPS it, `\r` returns to the start of
// only the LAST row, `\x1b[2K` clears only that row, and every earlier row
// stays. Each new word repaints a slightly longer block below the previous one.
//
// Half-duplex hid it: a clip is bounded, so the caption rarely outgrew a line.
// Full duplex has no bound — the microphone is open for as long as someone
// talks — so a single long sentence fills the screen.
//
// The fix is to never emit more than one row's worth. The TAIL is kept rather
// than the head: while a sentence is still arriving, the words just spoken are
// the ones worth showing.
package main

import (
	"fmt"

	"helix/internal/shell"
)

// hearingPrefix labels the row. Measured, not assumed, when budgeting.
const hearingPrefix = "[hearing] "

// paintHearing redraws the live transcript row in place.
func paintHearing(text string) {
	fmt.Print("\r\x1b[2K" + hearingLine(text))
}

// hearingLine renders the row, clamped to the real terminal.
func hearingLine(text string) string {
	return hearingLineAt(text, shell.TerminalWidth())
}

// hearingLineAt is the width arithmetic, with the measurement passed in.
//
// Separated because TerminalWidth probes real file descriptors and returns 0
// under `go test`, so a test calling hearingLine could only SKIP — and the
// whole defect was arithmetic. Three skipped tests are not coverage of a
// column-counting bug; §13 already records two of those (PanelWrap counting
// bytes, truncateANSI counting runes) found on screen rather than in the suite.
func hearingLineAt(text string, width int) string {
	if width <= 0 {
		// No terminal to measure (a pipe, a test). Emit the text unclamped:
		// there is no wrapping to cause and nothing to protect.
		return hearingPrefix + text
	}
	// One column short of the edge. Writing into the final cell makes some
	// terminals wrap immediately and others defer it, and the difference is
	// exactly the bug being fixed.
	budget := width - len(hearingPrefix) - 1
	if budget < 8 {
		// A terminal too narrow for a caption still gets the label, which at
		// least says the microphone is live.
		return hearingPrefix
	}
	return hearingPrefix + shell.TruncateTail(text, budget)
}

// internal/shell/main_test.go
//
// Purpose: force colour on for this package's tests.
//
// `go test` runs with stdout redirected to a pipe, so the colour gate correctly
// disables ANSI — and this package's tests are almost entirely ABOUT the ANSI:
// that widths are measured on visible cells rather than escape bytes, that a
// wrapped line reopens the colour it was cut in the middle of, that a truncated
// cell still emits its reset. Without forcing it, they would all pass against
// plain strings and prove nothing.
//
// CLICOLOR_FORCE is the same switch a user reaches for when piping Helix into
// `less -R`, so the tests exercise the real code path rather than a test-only
// one.
//
// THE WIDTH IS PINNED FOR THE SAME REASON, and it was learned the hard way.
// Panels wrap to the terminal, so any rendering assertion left to the ambient
// width is really an assertion about the window the suite ran in: green under
// CI, which has no terminal at all, and red at a release gate run from an
// 80-column one. Twelve tests across two packages failed that way, at 40, at
// 80, at 400 — each on output that was correct. Sampling widths cannot prove
// the absence of the next one, so the measure is fixed here instead: 0 models
// the unmeasurable terminal CI has, which is the condition every one of these
// tests was written against.
package shell

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := os.Setenv("CLICOLOR_FORCE", "1"); err != nil {
		panic(err)
	}
	restore := PinWidth(0)
	code := m.Run()
	restore()
	os.Exit(code)
}

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
package shell

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if err := os.Setenv("CLICOLOR_FORCE", "1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

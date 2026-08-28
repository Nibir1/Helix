// cmd/helix/main_test.go
//
// Purpose: force colour on for this package's tests, for the same reason
// internal/shell does — several of them assert on the rendered escapes, and
// `go test` redirects stdout to a pipe where the colour gate correctly turns
// ANSI off.
//
// TestVoicePrompterSpeaksPlainTextNotEscapes is the one that makes this
// load-bearing rather than cosmetic: it exists to prove a panel-styled prompt
// is stripped before it reaches a TTS engine, and with colour disabled there
// would be nothing to strip, so it would pass while proving the opposite of
// what it claims.
package main

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

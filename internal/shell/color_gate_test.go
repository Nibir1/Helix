// internal/shell/color_gate_test.go
//
// Purpose: colour must not reach a destination that cannot render it.
//
// This exists because of an asymmetry that nearly shipped a regression.
// github.com/fatih/color disables itself when stdout is not a terminal, so
// every color.Cyan in the codebase had silently been doing the right thing when
// Helix was piped or run as a service. shell.Fg emitted escapes
// unconditionally — so converting the daemon's output to the panel language
// would have written control codes into journald where none appeared before:
// the polish making things worse in the one place nobody looks until something
// breaks.
package shell

import (
	"strings"
	"testing"
)

func TestColourIsOffWhenNothingCanRenderIt(t *testing.T) {
	cases := []struct {
		name                  string
		isTTY, force, noColor bool
		term                  string
		want                  bool
	}{
		{name: "a terminal", isTTY: true, term: "xterm-256color", want: true},
		{name: "a pipe", isTTY: false, term: "xterm-256color", want: false},
		// no-color.org: honoured however it is set, including empty.
		{name: "NO_COLOR on a terminal", isTTY: true, noColor: true, term: "xterm", want: false},
		{name: "TERM=dumb on a terminal", isTTY: true, term: "dumb", want: false},
		// The override, for `helix … | less -R` and for this package's tests.
		{name: "CLICOLOR_FORCE through a pipe", isTTY: false, force: true, term: "xterm", want: true},
		// FORCE beats NO_COLOR: the user asked for it more specifically than a
		// global opt-out, which is how every other tool resolves this pair.
		{name: "CLICOLOR_FORCE beats NO_COLOR", isTTY: false, force: true, noColor: true,
			term: "xterm", want: true},
		// A pipe with no TERM at all — cron, a CI runner, systemd.
		{name: "no TERM through a pipe", isTTY: false, term: "", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldColorize(tc.isTTY, tc.force, tc.noColor, tc.term); got != tc.want {
				t.Errorf("colour enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// And the primitives must actually honour it, or the rule is decorative.
func TestPrimitivesEmitNoEscapesWhenColourIsOff(t *testing.T) {
	for name, rendered := range map[string]string{
		"Fg":  fgIf(false, HexAmber, "value"),
		"Bg":  bgIf(false, HexAmber, "value"),
		"Seg": segIf(false, HexPrimary, HexVoid, " CHIP "),
	} {
		if strings.ContainsRune(rendered, 0x1b) {
			t.Errorf("%s emitted an escape with colour off: %q", name, rendered)
		}
		if !strings.Contains(rendered, "value") && !strings.Contains(rendered, "CHIP") {
			t.Errorf("%s dropped its text: %q", name, rendered)
		}
	}
}

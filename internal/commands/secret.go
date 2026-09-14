// internal/commands/secret.go
// Purpose: read a credential without putting it on the screen.
//
// THE INCIDENT THIS EXISTS FOR. API keys were read with AskLine — the same
// function that asks which provider you want — which echoes. A user pasted an
// OpenAI key into the speech wizard on Windows, the terminal printed it in
// full, and it landed in their scrollback, in the screenshot they sent, and in
// this repository's issue trail. The key had to be revoked.
//
// It was never Windows-specific. AskLine echoed on every platform from the day
// keys were first asked for; nobody had screenshotted macOS.
//
// NOT ROUTED THROUGH Prompter, and that is deliberate rather than lazy. A
// credential must never reach the voice prompter: ADR-005 puts /setup on the
// voice-denied list precisely because it "would have you dictate API keys
// aloud", and a secret that travels through the same abstraction as an ordinary
// question is one refactor away from doing exactly that. This reads the
// terminal directly, so there is no channel to misroute.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// AskSecret prompts for a credential and reads it without echoing.
//
// Falls back to a plain read when stdin is not a terminal — a pipe or a script,
// where nothing is echoing in the first place and refusing would break
// automation. That fallback ANNOUNCES itself, because the one environment where
// it matters is the one that caused the incident: under some terminal
// emulators (MSYS2's MINGW64 among them) a Go binary is handed a pipe rather
// than a console, echo is the emulator's to control and not ours, and silently
// reading would reproduce the original bug while looking fixed.
func AskSecret(prompt string) string {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Printf("%s (input is NOT hidden here — this terminal does not give Helix a console): ", prompt)
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return ""
		}
		return strings.TrimSpace(line)
	}

	fmt.Printf("%s (hidden): ", prompt)
	raw, err := term.ReadPassword(int(os.Stdin.Fd()))
	// ReadPassword consumes the newline the user typed but prints nothing, so
	// the cursor is still on the prompt line. Without this the next output
	// continues it.
	fmt.Println()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

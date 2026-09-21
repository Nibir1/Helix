// cmd/helix/keyprompt_view.go
// Purpose: asking for an API key, as a panel that answers the questions the
// asking raises.
//
// WHAT THIS REPLACES. One bare line at column zero, outside every convention
// the rest of the shell follows:
//
//	Paste API key for openai (hidden):
//
// It assumed the reader knew three things it never said. Where do I get one —
// a URL nobody can guess, since Groq's console is not on groq.com's front page
// and Gemini keys come from AI Studio. Where does it go — a credential is
// exactly the thing people want to know the destination of before they paste
// it. And is it safe to type here — the prompt hides input, which is invisible
// precisely because it works, so it is worth saying.
//
// Lines, not prints, so the layout can be rendered in a test and read (§9
// rule 12).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"helix/internal/providers"
	"helix/internal/shell"
)

// keyPromptLines renders the panel body shown before a key is asked for.
//
// hidden reports whether the terminal will actually suppress echo. It is a
// parameter rather than a lookup because the honest answer differs per
// terminal — MSYS2 hands a Go binary a pipe, where echo belongs to the emulator
// — and a panel that claims input is hidden when it is not is worse than one
// that says nothing.
func keyPromptLines(provider, storePath string, hidden bool) []string {
	rows := [][2]string{{"PROVIDER", provider}}
	if url := providers.KeyConsoleURL(provider); url != "" {
		rows = append(rows, [2]string{"GET ONE", url})
	}
	rows = append(rows, [2]string{"STORED", storePath + "  (0600, this machine only)"})
	if env := providers.KeyEnvName(provider); env != "" {
		rows = append(rows, [2]string{"OR SET", "$" + env + "  (never written to disk)"})
	}

	labels := make([]string, 0, len(rows))
	for _, r := range rows {
		labels = append(labels, r[0])
	}
	width := shell.KVWidth(labels...)

	lines := make([]string, 0, len(rows)+4)
	for _, r := range rows {
		lines = append(lines, shell.KV(r[0], shell.Value(r[1]), width))
	}
	lines = append(lines, shell.PanelGap())

	// The safety sentence is last because it is reassurance, not instruction —
	// and it states only what is true of THIS terminal.
	note := "Nothing is echoed as you type. The key is never logged, never passed " +
		"as a command-line argument, and goes nowhere but " + provider + "."
	if !hidden {
		note = "This terminal does not give Helix a console, so it CANNOT hide what you " +
			"type — the key will be visible on screen and in your scrollback. Helix " +
			"still never logs it. Paste it elsewhere if that matters."
	}
	colour := shell.Muted
	if !hidden {
		colour = func(s string) string { return shell.Fg(shell.HexTertiary, s) }
	}
	lines = append(lines, shell.PanelWrap(note, colour)...)
	return lines
}

// secretsStoreLabel is where keys land, written the way a user reads a path.
func secretsStoreLabel() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return "~/.helix/secrets.json"
	}
	return filepath.Join(".helix", "secrets.json")
}

// printKeyPrompt renders the panel. The reading is the caller's job; this
// deliberately does not read, so the thing that suppresses echo stays in one
// place (internal/commands.AskSecret) and cannot be duplicated here.
func printKeyPrompt(provider, storePath string, hidden bool) {
	fmt.Println(shell.PanelTitle("api key"))
	for _, l := range keyPromptLines(provider, storePath, hidden) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())
}

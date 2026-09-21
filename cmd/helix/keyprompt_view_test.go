// cmd/helix/keyprompt_view_test.go
// Purpose: the key panel is rendered, so it is verified by rendering it.
//
// It replaces one bare line at column zero — "Paste API key for openai
// (hidden):" — that assumed the reader knew three things it never said: where
// to get a key, where the pasted one goes, and whether it is safe to type here.
package main

import (
	"strings"
	"testing"

	"helix/internal/providers"
	"helix/internal/shell"
)

func renderKeyPanel(t *testing.T, provider string, hidden bool) string {
	t.Helper()
	return shell.Plain(strings.Join(keyPromptLines(provider, "~/.helix/secrets.json", hidden), "\n"))
}

// The three questions the old prompt left unanswered.
func TestKeyPanelAnswersWhereToGetWhereItGoesAndWhetherItIsSafe(t *testing.T) {
	// Flattened: these are assertions about what the panel SAYS, and a panel
	// wraps to the terminal — on a narrow one "never logged" breaks across
	// the gutter and a substring search stops finding a sentence that is
	// plainly there.
	out := flattenPanel(renderKeyPanel(t, "openai", true))

	if !strings.Contains(out, "platform.openai.com") {
		t.Error("the panel does not say where to get a key; the URL is not guessable")
	}
	if !strings.Contains(out, "secrets.json") || !strings.Contains(out, "0600") {
		t.Error("the panel does not say where the key is stored, which is the thing " +
			"people want to know BEFORE pasting a credential")
	}
	if !strings.Contains(out, "never logged") {
		t.Error("the panel does not say the key is not logged")
	}
	if !strings.Contains(out, "OPENAI_API_KEY") {
		t.Error("the panel does not offer the environment variable, which is the way " +
			"to avoid writing a key to disk at all")
	}
}

// THE ONE THAT MATTERS. On a terminal that cannot suppress echo, promising the
// key will not appear on screen is worse than saying nothing — the user pastes
// a credential on the strength of it. This is the exact situation that put a
// live key in a screenshot.
func TestKeyPanelNeverPromisesHidingItCannotDo(t *testing.T) {
	visible := flattenPanel(renderKeyPanel(t, "openai", false))

	if strings.Contains(visible, "Nothing is echoed") {
		t.Fatal("the panel promises hidden input on a terminal that cannot hide it")
	}
	for _, want := range []string{"CANNOT hide", "visible on screen", "scrollback"} {
		if !strings.Contains(visible, want) {
			t.Errorf("the warning never says %q:\n%s", want, visible)
		}
	}
	// And it still says what remains true.
	if !strings.Contains(visible, "never logs it") {
		t.Error("the warning drops the reassurance that is still true")
	}

	hidden := flattenPanel(renderKeyPanel(t, "openai", true))
	if strings.Contains(hidden, "CANNOT hide") {
		t.Error("a terminal that DOES hide input is warned as though it does not")
	}
}

// A wrong URL is worse than no URL: the reader trusts it, follows it, and has
// to work out that Helix was wrong rather than their memory.
func TestProvidersWithNoConsoleGetNoURL(t *testing.T) {
	out := renderKeyPanel(t, "custom", true)
	if strings.Contains(out, "GET ONE") {
		t.Error("a provider with no key console was given one anyway")
	}
	// The rest of the panel still renders.
	if !strings.Contains(out, "secrets.json") {
		t.Error("dropping the URL dropped the rest of the panel")
	}
}

// Speech providers share their chat sibling's account, so they must not be
// sent to a page that does not exist.
func TestSpeechProvidersInheritTheirVendorConsole(t *testing.T) {
	for _, c := range [][2]string{
		{"stt.openai", "platform.openai.com"},
		{"tts.elevenlabs", "elevenlabs.io"},
		{"stt.groq", "console.groq.com"},
	} {
		if got := providers.KeyConsoleURL(c[0]); !strings.Contains(got, c[1]) {
			t.Errorf("KeyConsoleURL(%q) = %q, want it to point at %q", c[0], got, c[1])
		}
	}
}

// Every provider Helix can ask a key for should either have a console or
// deliberately have none — a silent "" for a real vendor is a gap.
func TestEveryChatProviderHasAConsole(t *testing.T) {
	for _, p := range []string{
		"openai", "anthropic", "deepseek", "gemini", "xai", "kimi", "qwen", "glm", "meta",
	} {
		if providers.KeyConsoleURL(p) == "" {
			t.Errorf("%s has no key console URL, so its prompt cannot say where to get one", p)
		}
	}
	if providers.KeyConsoleURL("custom") != "" {
		t.Error(`"custom" was given a console URL; there is no such page`)
	}
}

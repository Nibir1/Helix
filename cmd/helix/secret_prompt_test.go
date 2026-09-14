// cmd/helix/secret_prompt_test.go
// Purpose: keep credentials off the screen at every place one is asked for.
//
// A user pasted an OpenAI key into the speech wizard, the terminal printed it,
// and it survived in scrollback and in a screenshot. The cause was not a bug in
// AskLine — AskLine is supposed to echo, it asks which provider you want. The
// cause was that a credential was asked for with it. That mistake is invisible
// at the call site: `commands.AskLine("API key for openai")` looks exactly like
// every other prompt in the file.
//
// So the guard is here rather than in a review checklist.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Wording that means the answer is a secret. Anything asked with these words
// must not travel through an echoing reader.
var credentialWording = regexp.MustCompile(`(?i)\b(api[ _-]?key|token|password|secret|credential|passphrase)\b`)

// askLineCall finds `commands.AskLine(` and returns the argument text, which
// may continue onto following lines — several call sites in this package wrap
// after the open paren.
var askLineCall = regexp.MustCompile(`commands\.AskLine\(`)

func helixSources(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		t.Fatal("no sources found — this test would pass vacuously")
	}
	return out
}

// The general rule: no prompt that asks for a secret may be read with the
// echoing reader. This catches the NEXT provider someone adds, which is the
// one the incident report cannot warn about.
func TestNoCredentialIsReadWithAnEchoingPrompt(t *testing.T) {
	for _, path := range helixSources(t) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		lines := strings.Split(string(src), "\n")
		for i, line := range lines {
			if !askLineCall.MatchString(line) {
				continue
			}
			// The prompt may wrap; look at this line and the next two.
			window := line
			for j := i + 1; j < len(lines) && j <= i+2; j++ {
				window += " " + lines[j]
			}
			if credentialWording.MatchString(window) {
				t.Errorf("%s:%d asks for a credential with commands.AskLine, which echoes:\n\t%s\n"+
					"use commands.AskSecret so the value never reaches the screen",
					path, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// The specific rule: the two sites the incident actually burned. Anchored on
// the assignment, so renaming the prompt text cannot quietly satisfy it.
func TestApiKeyPromptsUseAskSecret(t *testing.T) {
	sites := []struct {
		file string
		what string
	}{
		{"helpers.go", "the provider setup wizard"},
		{"speech_handlers.go", "the speech setup wizard"},
	}
	assign := regexp.MustCompile(`key\s*:?=\s*commands\.(AskSecret|AskLine)\(`)

	for _, s := range sites {
		src, err := os.ReadFile(s.file)
		if err != nil {
			t.Fatalf("read %s: %v", s.file, err)
		}
		matches := assign.FindAllStringSubmatch(string(src), -1)
		if len(matches) == 0 {
			t.Errorf("%s: no `key := commands.Ask...` call found — %s no longer "+
				"reads a key here, or it was renamed. Re-point this guard.", s.file, s.what)
			continue
		}
		for _, m := range matches {
			if m[1] != "AskSecret" {
				t.Errorf("%s: %s reads the API key with commands.%s, which echoes it "+
					"to the terminal, into scrollback, and into any screenshot",
					s.file, s.what, m[1])
			}
		}
	}
}

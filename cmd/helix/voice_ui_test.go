// cmd/helix/voice_ui_test.go
// Purpose: the wizard's rendering has two things it must not break — the shell
// command a user is told to run, and the question a TTS engine is told to speak.
package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"helix/internal/shell"
	"helix/internal/speech"
)

// capture runs f with stdout redirected and returns what it printed.
func capture(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	f()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}

// The diagnosis format is statement at column 0, reasoning indented two, and
// the command to run indented four. Word-wrapping all of it collapses the
// structure and breaks the command across lines — a command nobody can copy.
// This is the exact defect the status screen already carries a comment about,
// and rendering this screen reproduced it immediately.
func TestWizDetailKeepsACommandOnOneLine(t *testing.T) {
	const cmd = "whisper-server -m /Users/somebody/.helix/whisper-models/ggml-small.en.bin --port 8080"
	out := capture(t, func() {
		wizDetail("whisper-local at http://127.0.0.1:8080: nothing is listening.\n" +
			"  Start it:\n" +
			"    " + cmd)
	})

	if !strings.Contains(shell.Plain(out), cmd) {
		t.Errorf("the launch command did not survive rendering:\n%s", shell.Plain(out))
	}
	// And every line still belongs to the panel.
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(shell.Plain(line), "  │ ") {
			t.Errorf("line escaped the gutter: %q", shell.Plain(line))
		}
	}
}

// Prose, by contrast, SHOULD wrap — otherwise a long sentence runs past the
// rule and its tail restarts at column zero.
func TestWizDetailWrapsProse(t *testing.T) {
	long := strings.Repeat("something is listening on that port and refused the request ", 4)
	out := capture(t, func() { wizDetail(long) })
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; n < 2 {
		t.Errorf("a %d-char sentence rendered as %d line(s)", len(long), n)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := len(shell.Plain(line)); w > shell.PanelRuleWidth()+4 {
			t.Errorf("line is %d cells, wider than the panel: %q", w, shell.Plain(line))
		}
	}
}

// A blank line inside a diagnosis must not open a hole in the gutter.
func TestWizDetailSkipsBlankLines(t *testing.T) {
	out := capture(t, func() { wizDetail("first\n\n   \nsecond\n") })
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n") + 1; n != 2 {
		t.Errorf("expected 2 rendered lines, got %d:\n%s", n, shell.Plain(out))
	}
}

// The wizard now asks its questions in the panel language, which means the
// question string carries ANSI. The voice prompter hands whatever it is given
// straight to the TTS engine — so without stripping, Helix would READ THE
// ESCAPE SEQUENCES ALOUD, and the answer to a spoken question nobody can parse
// is a fail-closed "no".
func TestVoicePrompterSpeaksPlainTextNotEscapes(t *testing.T) {
	var spoken string
	v := &VoicePrompter{
		Speak:   func(text string) { spoken = text },
		Listen:  func(context.Context) (speech.Transcript, error) { return speech.Transcript{Text: "yes"}, nil },
		Timeout: time.Second,
	}

	styled := shell.Prompt("start whisper-local now", "")
	if !strings.ContainsRune(styled, 0x1b) {
		t.Fatal("the panel prompt is expected to carry colour; this test is pointless without it")
	}
	if !v.AskYesNo(styled) {
		t.Fatal("a clear yes must be accepted")
	}
	if strings.ContainsRune(spoken, 0x1b) {
		t.Errorf("an escape sequence reached the TTS engine: %q", spoken)
	}
	if !strings.Contains(spoken, "start whisper-local now") {
		t.Errorf("the question itself did not reach the TTS engine: %q", spoken)
	}
}

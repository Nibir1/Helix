// internal/commands/secret_test.go
// Purpose: hold the line that the key-echo incident crossed.
package commands

import (
	"os"
	"strings"
	"testing"
)

// withStdin replaces os.Stdin with a pipe carrying payload, and captures
// everything AskSecret prints while it reads. Returns (result, printed).
//
// A pipe is not a terminal, so this exercises the FALLBACK branch. That is the
// branch worth pinning: the TTY branch cannot be driven from `go test` (there
// is no console attached), and the fallback is the one that runs in the exact
// environment where the incident happened — MSYS2 hands Go a pipe.
func withStdin(t *testing.T, payload string, fn func() string) (string, string) {
	t.Helper()

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}

	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	defer func() { os.Stdin, os.Stdout = origIn, origOut }()

	go func() {
		_, _ = inW.WriteString(payload)
		_ = inW.Close()
	}()

	done := make(chan string, 1)
	go func() { done <- fn() }()
	got := <-done

	_ = outW.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, rerr := outR.Read(buf)
		sb.Write(buf[:n])
		if rerr != nil {
			break
		}
	}
	_ = outR.Close()
	_ = inR.Close()

	return got, sb.String()
}

// A pipe cannot hide anything — echo there belongs to whatever is on the other
// end, not to us. So AskSecret must SAY so rather than pretend, otherwise the
// original bug survives behind a function whose name claims it is fixed.
func TestAskSecretAnnouncesWhenItCannotHide(t *testing.T) {
	got, printed := withStdin(t, "sk-proj-notreal\n", func() string {
		return AskSecret("API key for openai")
	})

	if got != "sk-proj-notreal" {
		t.Fatalf("AskSecret returned %q, want the pasted key", got)
	}
	if !strings.Contains(printed, "API key for openai") {
		t.Errorf("prompt text missing from output:\n%s", printed)
	}
	if !strings.Contains(printed, "NOT hidden") {
		t.Errorf("no warning that input is visible here; a user would believe\n"+
			"the key was hidden when it was not. Got:\n%s", printed)
	}
	// The whole point: the secret itself must never be written back out.
	if strings.Contains(printed, "sk-proj-notreal") {
		t.Errorf("AskSecret echoed the secret it was given:\n%s", printed)
	}
}

// Surrounding whitespace is what a paste carries. A key with a trailing \r
// (Windows clipboards produce them) is not the same string to an HTTP header.
func TestAskSecretTrimsThePaste(t *testing.T) {
	got, _ := withStdin(t, "  sk-proj-padded \r\n", func() string {
		return AskSecret("key")
	})
	if got != "sk-proj-padded" {
		t.Fatalf("AskSecret returned %q, want it trimmed to %q", got, "sk-proj-padded")
	}
}

// Closed stdin with nothing on it is a cancelled setup, not an empty key.
func TestAskSecretReturnsEmptyOnEOF(t *testing.T) {
	got, _ := withStdin(t, "", func() string { return AskSecret("key") })
	if got != "" {
		t.Fatalf("AskSecret returned %q on EOF, want empty", got)
	}
}

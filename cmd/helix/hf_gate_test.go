// cmd/helix/hf_gate_test.go
//
// Purpose: the parts of the licence gate that can be checked without an
// account, a browser, or a network.
//
// The gate itself is a human decision and no test can stand in for it. What
// CAN be pinned is that Helix reaches that decision correctly: the right CLI
// names, a pip command that survives being exec'd with no shell, and a browser
// opener that is right per platform rather than right on the author's laptop.
package main

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// Both entry-point names must be accepted. huggingface_hub renamed the CLI to
// `hf`; pinning only the new name tells someone with a working install that
// they have nothing.
func TestBothHuggingFaceCLINamesAreAccepted(t *testing.T) {
	names := map[string]bool{}
	for _, n := range hfBinaryNames {
		names[n] = true
	}
	for _, want := range []string{"hf", "huggingface-cli"} {
		if !names[want] {
			t.Errorf("%q is not looked for; a host with only that name would be "+
				"told to install what it already has", want)
		}
	}
}

// The pip argument must not be quoted.
//
// runVisibleCommand splits on spaces and execs directly — there is no shell to
// strip quotes, so `"huggingface_hub[cli]"` would be passed to pip WITH the
// quotes and fail as a package name. This is the same class of bug as the
// RUSTFLAGS assignment that could not be written into a command string.
func TestPipTargetIsNotShellQuoted(t *testing.T) {
	const target = "huggingface_hub[cli]"
	src := readSourceFile(t, "hf_gate.go")
	fn := functionBody(src, "func installHuggingFaceCLI(")
	if fn == "" {
		t.Fatal("installHuggingFaceCLI not found")
	}
	if !strings.Contains(fn, target) {
		t.Fatalf("the pip command does not install %s", target)
	}
	for _, quoted := range []string{`\"` + target + `\"`, `'` + target + `'`} {
		if strings.Contains(fn, quoted) {
			t.Errorf("the pip target is quoted (%s); there is no shell here, so "+
				"the quotes become part of the package name", quoted)
		}
	}
}

// The opener must be the one this platform actually has.
func TestBrowserOpenerMatchesThePlatform(t *testing.T) {
	src := readSourceFile(t, "hf_gate.go")
	fn := functionBody(src, "func openInBrowser(")
	if fn == "" {
		t.Fatal("openInBrowser not found")
	}
	for _, want := range []string{"open", "xdg-open", "rundll32"} {
		if !strings.Contains(fn, want) {
			t.Errorf("openInBrowser never mentions %q", want)
		}
	}
	// `cmd /c start <url>` treats a first quoted argument as the window TITLE,
	// which is a classic way to open a blank window instead of the page.
	//
	// Checked against CODE, not the file: the comment in openInBrowser explains
	// why cmd/start is avoided, and a naive grep read its own explanation as
	// the thing it was warning about. A guard that fires on the reason it
	// exists is worse than none.
	code := stripComments(fn)
	if strings.Contains(code, "start") && strings.Contains(code, "cmd") {
		t.Error("openInBrowser uses `cmd /c start`, where the URL can be taken " +
			"as a window title; rundll32 has no such ambiguity")
	}
	// It must never block a wizard: a browser that does not exit is not the
	// install's problem.
	if !strings.Contains(code, ".Start()") {
		t.Error("openInBrowser waits for the browser to exit; it must Start and " +
			"move on")
	}
}

// Opening a browser must never be fatal: a headless box or an SSH session has
// none, and the URL is printed either way.
func TestBrowserOpenIsBestEffort(t *testing.T) {
	// A scheme nothing handles: the call must return, not panic or hang.
	done := make(chan bool, 1)
	go func() { done <- openInBrowser("helix-test-no-such-scheme://nowhere") }()
	select {
	case <-done:
	case <-timeoutAfterSeconds(20):
		t.Fatal("openInBrowser did not return within 20s")
	}
	if runtime.GOOS == "" {
		t.Skip()
	}
}

// The gate must still name the manual path when it cannot help.
func TestGateFallsBackToTellingTheUser(t *testing.T) {
	src := readSourceFile(t, "hf_gate.go")
	fn := functionBody(src, "func settleCSMWeights(")
	if !strings.Contains(fn, "reportCSMWeightsGate()") {
		t.Error("when the CLI cannot be installed, settleCSMWeights must fall " +
			"back to printing the steps rather than leaving the user with nothing")
	}
	if !strings.Contains(fn, "hfLoggedIn(") {
		t.Error("settleCSMWeights does not check for an existing login, so a " +
			"user who signed in last week is walked through it again")
	}
}

func timeoutAfterSeconds(n int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(n) * time.Second)
		close(ch)
	}()
	return ch
}

// stripComments removes // lines and trailing // comments, so a source check
// tests the code rather than the prose beside it.
func stripComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			continue
		}
		// Trailing comment, taking the naive view that // inside a string
		// literal does not occur in the functions this inspects. It does occur
		// in URLs, so anything with a scheme is left whole.
		if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line, "://") {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

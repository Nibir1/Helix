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

// The CLI's command shape is DETECTED, never inferred from its name.
//
// huggingface_hub 1.x moved authentication under an `auth` group, so
// `hf auth login` is the current form and `hf login` produces
// "Error: No such command 'login'." — which is exactly what a fresh install
// produced. The name is not the version, either: 1.x installs both `hf` and a
// `huggingface-cli` that only prints "deprecated and no longer works".
func TestAuthCommandShapeIsProbedNotGuessed(t *testing.T) {
	src := readSourceFile(t, "hf_gate.go")
	code := stripComments(src)

	if !strings.Contains(code, `"auth", "--help"`) {
		t.Error("the auth group is not probed; the CLI's shape would be a guess")
	}
	// The old form must never be hardcoded at a call site.
	for _, bad := range []string{`{bin, "login"}`, `bin + " login"`} {
		if strings.Contains(code, bad) {
			t.Errorf("a login command is built as %s, bypassing hfAuthArgv — on "+
				"huggingface_hub 1.x that is \"No such command 'login'\"", bad)
		}
	}
	fn := functionBody(src, "func hfAuthArgv(")
	if fn == "" {
		t.Fatal("hfAuthArgv not found")
	}
	for _, want := range []string{`"auth"`, "verb"} {
		if !strings.Contains(fn, want) {
			t.Errorf("hfAuthArgv does not mention %s", want)
		}
	}
}

// Both shapes must be constructible, whichever CLI a host has.
func TestAuthArgvCoversBothCLIGenerations(t *testing.T) {
	// A binary that cannot run at all: the probe fails, so the 0.x shape is
	// used. That is the safe default — it is what every pre-1.0 install wants.
	argv := hfAuthArgv("helix-no-such-binary-xyz", "login")
	if len(argv) != 2 || argv[1] != "login" {
		t.Errorf("with no auth group, argv = %v, want [<bin> login]", argv)
	}

	// And on this host, if a real CLI is present, whichever shape it declares.
	bin, ok := findHuggingFaceCLI()
	if !ok {
		t.Skip("no hf CLI on this machine")
	}
	got := hfAuthArgv(bin, "login")
	if hfHasAuthGroup(bin) {
		if len(got) != 3 || got[1] != "auth" || got[2] != "login" {
			t.Errorf("this CLI has an auth group but argv = %v", got)
		}
	} else if len(got) != 2 || got[1] != "login" {
		t.Errorf("this CLI has no auth group but argv = %v", got)
	}
}

// A sidecar that cannot serve must not be started to fail.
func TestCSMRefusesToStartWithoutCredentials(t *testing.T) {
	sc, ok := voiceSidecars()["csm-local"]
	if !ok {
		t.Fatal("csm-local is not in the sidecar table")
	}
	if sc.PreStart == nil {
		t.Fatal("csm-local has no PreStart check — it is launched without a " +
			"Hugging Face token and dies with 401 Unauthorized fetching its " +
			"tokenizer, so the user reads a crash instead of the cause")
	}
	reason, blocked := sc.PreStart()
	if bin, have := findHuggingFaceCLI(); have && hfLoggedIn(bin) {
		if blocked {
			t.Errorf("signed in, but csm-local is still blocked: %s", reason)
		}
		return
	}
	if !blocked {
		t.Error("not signed in, yet csm-local reports it can start")
		return
	}
	// The reason has to name the fix, not just the failure.
	for _, want := range []string{"401", csmModelURL, "login"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not mention %q: %s", want, reason)
		}
	}
}

// The weights must be fetched BEFORE the readiness check, not during it.
//
// csm.rs downloads sesame/csm-1b on first run, inside the window Helix measures
// as "did the server come up?". A ~2GB fetch blew a 90-second budget and Helix
// declared a sidecar dead while its own log showed it working:
//
//	[INFO csm_rs] Fetching model from Hugging Face Hub: sesame/csm-1b
//
// kokoro-local taught this once already, with `docker run` pulling a
// multi-gigabyte image inside the same window. The lesson is that a readiness
// budget must measure readiness.
func TestWeightsAreFetchedBeforeStartup(t *testing.T) {
	src := readSourceFile(t, "hf_gate.go")
	fn := functionBody(src, "func settleCSMWeights(")
	if fn == "" {
		t.Fatal("settleCSMWeights not found")
	}
	if !strings.Contains(stripComments(fn), "fetchCSMWeights(") {
		t.Error("settleCSMWeights does not fetch the weights, so the first-run " +
			"download happens inside the readiness budget and the sidecar is " +
			"reported dead while it is downloading")
	}

	fetch := functionBody(src, "func fetchCSMWeights(")
	if fetch == "" {
		t.Fatal("fetchCSMWeights not found")
	}
	code := stripComments(fetch)
	if !strings.Contains(code, `"download"`) {
		t.Error("fetchCSMWeights does not run the CLI's download subcommand")
	}
	if !strings.Contains(code, "csmModelRepo") {
		t.Error("fetchCSMWeights does not name the repo it is fetching")
	}
	// A failed pre-fetch must not block the sidecar: csm.rs retries on its own.
	if !strings.Contains(code, "StateWarn") {
		t.Error("a failed pre-fetch is treated as fatal; csm.rs will try again " +
			"on first run, so this is a warning")
	}
}

// An already-built csm must still reach the licence and weights steps.
//
// A previous run can have built the server and stopped at the login; returning
// early on "already built" would skip both forever.
func TestAlreadyBuiltCSMStillSettlesWeights(t *testing.T) {
	src := readSourceFile(t, "csm_install.go")
	fn := functionBody(src, "func installCSMServer(")
	if fn == "" {
		t.Fatal("installCSMServer not found")
	}
	head := fn
	if i := strings.Index(head, "feature, why := csmBackend()"); i > 0 {
		head = head[:i] // the early-return branch only
	}
	if !strings.Contains(stripComments(head), "settleCSMWeights()") {
		t.Error("the already-built path returns without settling the licence or " +
			"the weights, so a run that stopped at the login can never finish")
	}
}

// The readiness budget must allow for loading a 1B model off disk.
func TestCSMGetsAStartupBudgetForLoadingAModel(t *testing.T) {
	src := readSourceFile(t, "voice_sidecars.go")
	fn := functionBody(src, "func startVoiceSidecar(")
	if !strings.Contains(stripComments(fn), `case "csm-local"`) {
		t.Error("csm-local uses the default startup budget; a 1B model has to be " +
			"read off disk and mapped before its port answers")
	}
}

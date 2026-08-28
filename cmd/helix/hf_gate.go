// cmd/helix/hf_gate.go
//
// Purpose: walk the user through CSM's licence gate instead of printing it.
//
// The weights for sesame/csm-1b are gated: they download only for an account
// that has accepted Sesame's terms. Helix cannot accept a licence for someone —
// that is consent tied to a person, and it is the one part of this install that
// stays theirs. But "cannot accept the terms" was quietly doing duty for
// "cannot help at all", and those are different:
//
//   - Installing the Hugging Face CLI is not a consent decision. It is a pip
//     package, and leaving the user to discover they need it is the same
//     "there is no single install command" dead end the wizard was just cured of.
//   - OPENING the terms page is not consent either. It puts the decision in
//     front of the person who has to make it, rather than leaving a URL on a
//     screen they have already scrolled past.
//   - Running `huggingface-cli login` is not consent. It is a prompt; the token
//     the user pastes into it is the consent.
//
// So Helix does all three and stops exactly where a human has to decide. The
// gate is still a gate — nothing here accepts anything — but the user arrives
// at it rather than being told where it is.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"helix/internal/shell"
)

const (
	// csmModelURL is the page where Sesame's terms are accepted.
	csmModelURL = "https://huggingface.co/sesame/csm-1b"

	hfLoginTimeout = 10 * time.Minute
	hfCheckTimeout = 20 * time.Second
)

// hfBinaryNames are the CLI's names, newest first.
//
// huggingface_hub renamed the entry point to `hf`; `huggingface-cli` still
// exists and still works, and plenty of installed versions only have that. Both
// are accepted rather than pinning the newer name and telling someone with a
// working install that they have nothing.
var hfBinaryNames = []string{"hf", "huggingface-cli"}

// findHuggingFaceCLI returns the CLI's path on this host.
//
// PATH first, then the directory pip installs user scripts into — because that
// directory is very often NOT on PATH, and pip says so itself:
//
//	WARNING: The scripts hf, huggingface-cli and tiny-agents are installed in
//	'/Users/you/Library/Python/3.14/bin' which is not on PATH.
//
// Helix used to install the CLI, fail to find it a second later, and tell the
// user to open a new shell — having just been told exactly where the thing was.
// A tool Helix installed is a tool Helix can locate.
func findHuggingFaceCLI() (string, bool) {
	for _, name := range hfBinaryNames {
		if p, err := exec.LookPath(name); err == nil {
			return p, true
		}
	}
	for _, dir := range pythonUserScriptDirs() {
		for _, name := range hfBinaryNames {
			p := filepath.Join(dir, name)
			if isRunnableFile(p) {
				return p, true
			}
		}
	}
	return "", false
}

// pythonUserScriptDirs returns where `pip install --user` puts executables.
//
// Asked of the interpreter rather than assumed: the layout differs by platform
// (bin vs Scripts) and by Python version (the 3.14 in the path above), and
// guessing either wrong reproduces the bug this exists to fix.
func pythonUserScriptDirs() []string {
	var out []string
	seen := map[string]bool{}
	for _, py := range []string{"python3", "python"} {
		bin, err := exec.LookPath(py)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		raw, err := exec.CommandContext(ctx, bin, "-m", "site", "--user-base").Output() //nolint:gosec // resolved from PATH
		cancel()
		if err != nil {
			continue
		}
		base := strings.TrimSpace(string(raw))
		if base == "" {
			continue
		}
		leaf := "bin"
		if runtime.GOOS == "windows" {
			leaf = "Scripts"
		}
		dir := filepath.Join(base, leaf)
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	return out
}

// hfLoggedIn reports whether the CLI already has a token.
//
// Checked before doing anything, because a user who logged in last week should
// not be walked through a browser and a token prompt again.
func hfLoggedIn(bin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), hfCheckTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "whoami").CombinedOutput() //nolint:gosec // resolved from PATH
	if err != nil {
		return false
	}
	// `whoami` prints the username when authenticated and "Not logged in"
	// otherwise, exiting non-zero on some versions and zero on others — so the
	// text decides rather than the status.
	s := strings.TrimSpace(string(out))
	return s != "" && !strings.Contains(strings.ToLower(s), "not logged in")
}

// installHuggingFaceCLI installs the CLI with pip, returning its path.
func installHuggingFaceCLI() (string, bool) {
	python, ok := findFirstBinary([]string{"python3", "python"})
	if !ok {
		// Reuse the catalogue rather than a bespoke message: it knows the
		// package name per manager and refuses to guess where it does not.
		if installed := installSidecarPrereqs("the Hugging Face CLI",
			voiceSidecar{Prereqs: []string{"python3"}}); !installed {
			fmt.Println(shell.Step(shell.StateBad, "huggingface-cli",
				"needs Python, which could not be installed here"))
			return "", false
		}
		if python, ok = findFirstBinary([]string{"python3", "python"}); !ok {
			return "", false
		}
	}

	fmt.Println(shell.Step(shell.StateWarn, "huggingface-cli",
		"not installed — installing it"))
	// No quotes around huggingface_hub[cli]: this is exec'd with no shell, so
	// quoting would make the brackets part of the package name.
	if !runVisibleArgv(
		[]string{python, "-m", "pip", "install", "--user", "huggingface_hub[cli]"},
		"", nil, 15*time.Minute) {
		return "", false
	}
	bin, found := findHuggingFaceCLI()
	if !found {
		fmt.Println(shell.Step(shell.StateWarn, "huggingface-cli",
			"installed, but cannot be located"))
		for _, l := range shell.StepDetail(
			"pip reported success and the binary is not on PATH or in python's "+
				"user script directory. Open a new shell and re-run /blackbox setup.",
			shell.Muted) {
			fmt.Println(l)
		}
		return "", false
	}
	fmt.Println(shell.Step(shell.StateGood, "huggingface-cli", "installed"))
	if _, err := exec.LookPath(filepath.Base(bin)); err != nil {
		// Found, but only because Helix knew where to look. Say so: every other
		// tool the user runs by name will still not find it.
		for _, l := range shell.StepDetail(
			"not on your PATH — Helix will use "+bin+". To use it yourself, add "+
				filepath.Dir(bin)+" to PATH.", shell.Muted) {
			fmt.Println(l)
		}
	}
	return bin, true
}

// openInBrowser asks the desktop to open a URL.
//
// Best effort by design: a headless box, an SSH session or a locked-down
// desktop has no browser to open, and that is not a failure of the install —
// the URL is printed either way, so the step degrades to what it used to do.
func openInBrowser(url string) bool {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		// rundll32 avoids `cmd /c start`, where the first quoted argument is
		// taken as the window TITLE and the URL silently becomes the second.
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	if _, err := exec.LookPath(name); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Start() == nil //nolint:gosec // fixed opener, URL is a constant
}

// settleCSMWeights takes the user as far as consent, and no further.
func settleCSMWeights() {
	fmt.Println()
	fmt.Println(shell.PanelTitle("csm weights"))
	for _, l := range shell.PanelWrap(
		"CSM's weights are gated: they download only for an account that has "+
			"accepted Sesame's terms. Helix installs the tool and opens the page, "+
			"because neither of those is the decision — accepting the licence is, "+
			"and that one is yours.", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())

	bin, ok := findHuggingFaceCLI()
	if !ok {
		if bin, ok = installHuggingFaceCLI(); !ok {
			// Fall back to telling them, which is where this started.
			reportCSMWeightsGate()
			return
		}
	}

	if hfLoggedIn(bin) {
		fmt.Println(shell.Step(shell.StateGood, "hugging face", "already signed in"))
		fmt.Println(shell.Step(shell.StateIdle, "terms",
			"accept them once at "+csmModelURL+" if you have not"))
		return
	}

	fmt.Println(shell.Step(shell.StateIdle, "terms", "opening the model page"))
	fmt.Println(shell.StepCommand(csmModelURL))
	if !openInBrowser(csmModelURL) {
		fmt.Println(shell.Step(shell.StateWarn, "browser",
			"could not be opened here — visit the link above"))
	}

	fmt.Println(shell.Step(shell.StateIdle, "sign in",
		"accept the terms in the browser, then paste an access token below"))
	for _, l := range shell.StepDetail(
		"tokens live at https://huggingface.co/settings/tokens", shell.Muted) {
		fmt.Println(l)
	}

	if !runVisibleArgv([]string{bin, "login"}, "", nil, hfLoginTimeout) {
		fmt.Println(shell.Step(shell.StateWarn, "hugging face",
			"not signed in — csm.rs cannot fetch the weights until you are"))
		fmt.Println(shell.StepCommand(bin + " login"))
		return
	}
	if hfLoggedIn(bin) {
		fmt.Println(shell.Step(shell.StateGood, "hugging face", "signed in"))
		return
	}
	fmt.Println(shell.Step(shell.StateWarn, "hugging face",
		"the login command finished but no account is active"))
	fmt.Println(shell.StepCommand(bin + " login"))
}

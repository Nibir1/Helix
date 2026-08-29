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

	// csmModelRepo is the same model as a Hub repo id, for the CLI.
	csmModelRepo = "sesame/csm-1b"

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

// hfAuthArgv builds an authentication command for whichever CLI is installed.
//
// huggingface_hub 1.x moved these under an `auth` group: `hf auth login` and
// `hf auth whoami`. The 0.x form was `huggingface-cli login`. Hardcoding the
// old shape produced, on a fresh install of the CURRENT library:
//
//	Error: No such command 'login'.
//
// Which shape applies is DETECTED rather than inferred from the binary's name,
// because the name is not the version: huggingface_hub 1.x installs both `hf`
// and a `huggingface-cli` that only prints "deprecated and no longer works".
// `<bin> auth --help` exits 0 where the group exists and non-zero where it does
// not, which is the whole test.
func hfAuthArgv(bin, verb string) []string {
	if hfHasAuthGroup(bin) {
		return []string{bin, "auth", verb}
	}
	return []string{bin, verb}
}

// hfAuthGroup caches the probe: it runs a subprocess, and the answer cannot
// change while Helix is running.
var hfAuthGroup = map[string]bool{}

func hfHasAuthGroup(bin string) bool {
	if v, ok := hfAuthGroup[bin]; ok {
		return v
	}
	ctx, cancel := context.WithTimeout(context.Background(), hfCheckTimeout)
	defer cancel()
	err := exec.CommandContext(ctx, bin, "auth", "--help").Run() //nolint:gosec // resolved path
	has := err == nil
	hfAuthGroup[bin] = has
	return has
}

// hfLoggedIn reports whether the CLI already has a token.
//
// Checked before doing anything, because a user who logged in last week should
// not be walked through a browser and a token prompt again.
func hfLoggedIn(bin string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), hfCheckTimeout)
	defer cancel()
	argv := hfAuthArgv(bin, "whoami")
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput() //nolint:gosec // resolved path
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
		fetchCSMWeights(bin)
		return
	}

	fmt.Println(shell.Step(shell.StateIdle, "terms", "opening the model page"))
	fmt.Println(shell.StepCommand(csmModelURL))
	if !openInBrowser(csmModelURL) {
		fmt.Println(shell.Step(shell.StateWarn, "browser",
			"could not be opened here — visit the link above"))
	}

	// Hand over explicitly.
	//
	// What follows is the Hugging Face CLI's own interactive prompt — its
	// wording, its layout, its numbered choice — and it lands in the middle of
	// Helix's panels looking like Helix stopped drawing them. It cannot be
	// restyled: it is another program's stdout, and `hf auth login` has no flag
	// to skip the question. So the seam is NAMED instead of hidden, which is
	// the honest version of polish here — the user is told whose UI they are
	// about to be in and what to pick.
	fmt.Println()
	fmt.Println(shell.PanelTitle("over to hugging face"))
	for _, l := range shell.PanelWrap(
		"The next few lines are the Hugging Face CLI's own prompt, not Helix's. "+
			"It asks how you want to sign in — choosing the browser option is "+
			"easiest, and it will show you a short code to confirm. Helix picks "+
			"up again when it finishes.", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelLine(shell.Muted("if you prefer a token, they live at")))
	fmt.Println(shell.StepCommand("https://huggingface.co/settings/tokens"))
	fmt.Println(shell.PanelEnd())
	fmt.Println()

	if !runVisibleArgv(hfAuthArgv(bin, "login"), "", nil, hfLoginTimeout) {
		fmt.Println(shell.Step(shell.StateWarn, "hugging face",
			"not signed in — csm.rs cannot fetch the weights until you are"))
		fmt.Println(shell.StepCommand(strings.Join(hfAuthArgv(bin, "login"), " ")))
		return
	}
	fmt.Println()
	if hfLoggedIn(bin) {
		fmt.Println(shell.Step(shell.StateGood, "hugging face", "signed in"))
		fetchCSMWeights(bin)
		return
	}
	fmt.Println(shell.Step(shell.StateWarn, "hugging face",
		"the login command finished but no account is active"))
	fmt.Println(shell.StepCommand(strings.Join(hfAuthArgv(bin, "login"), " ")))
}

// csmWeightsTimeout bounds the model download. Several gigabytes over whatever
// connection the user has; generous, and still bounded.
const csmWeightsTimeout = 60 * time.Minute

// fetchCSMWeights pulls the model BEFORE the server is started.
//
// This is the same mistake kokoro already taught, in a different shape. csm.rs
// downloads sesame/csm-1b on first run, inside the window Helix was measuring
// as "did the server come up?" — so a ~2 GB fetch over a normal connection blew
// a 90-second readiness budget and Helix reported a sidecar as dead while its
// own log showed it working:
//
//	[INFO csm_rs] Fetching model from Hugging Face Hub: sesame/csm-1b
//
// Pulling it separately means the readiness check measures STARTUP, which is
// what it is for, and the download gets the CLI's own progress display instead
// of a spinner that says nothing about how far along it is.
//
// Run unconditionally rather than gated on a cache check: `hf download` is a
// fast no-op when the files are already local, and asking the CLI is more
// reliable than reproducing its cache layout — which moves with HF_HOME and
// HF_HUB_CACHE, and would be one more thing to get wrong.
func fetchCSMWeights(bin string) {
	fmt.Println()
	fmt.Println(shell.PanelTitle("fetching the weights"))
	for _, l := range shell.PanelWrap(
		"Several gigabytes on first run, cached afterwards. The CLI's bar counts "+
			"FILES, not bytes — the small config and tokenizer files finish in "+
			"seconds, so it pauses near the end while the model itself arrives. "+
			"That pause is the download, not a stall. Helix fetches only the "+
			"copy csm.rs loads — the repo also carries a second, sharded copy "+
			"for a library Helix does not use.", shell.Muted) {
		fmt.Println(l)
	}
	fmt.Println(shell.PanelEnd())
	fmt.Println()

	if !enoughDiskFor("the CSM weights", helixPath(""), csmWeightsDiskBytes) {
		return
	}

	// Streamed, not captured: see runVisibleStreamed. Through a pipe the CLI
	// cannot redraw, and a multi-gigabyte fetch looks frozen.
	argv := append([]string{bin, "download", csmModelRepo}, csmDownloadFilters()...)
	if !runVisibleStreamed(argv, "", nil, csmWeightsTimeout) {
		fmt.Println(shell.Step(shell.StateWarn, "weights",
			"could not be fetched — csm.rs will try again on first run"))
		return
	}
	fmt.Println()
	fmt.Println(shell.Step(shell.StateGood, "weights", "cached locally"))
}

// csmDownloadFilters keep the fetch to the copy of the model csm.rs loads.
//
// sesame/csm-1b ships the SAME weights THREE times:
//
//	model.safetensors                        6.2 GB  ← csm.rs loads this
//	transformers-0000{1,2}-of-00002.safetensors  8.4 GB  for the transformers library
//	ckpt.pt                                  4.9 GB  the original PyTorch checkpoint
//
// Downloading the repo whole is 19.6 GB where 6.2 will do.
//
// The exclusions are taken from the repo's ACTUAL file listing, which matters
// because the first attempt was not: it excluded "transformers.safetensors*",
// a name inferred from the index file `transformers.safetensors.index.json`.
// The shards are called `transformers-00001-of-00002.safetensors`, so the
// pattern matched only the index — 4 KB of the 13.3 GB it was meant to stop —
// and the user downloaded all three copies. Guessing a filename from a
// neighbouring filename is guessing.
//
// Excluding rather than allow-listing, still: an allow-list drops anything
// csm.rs needs that this does not know about, and that failure appears as a
// mystery at startup. This can only ever be wrong toward downloading too much.
// What csm.rs actually needs is known from evidence — left alone, it fetched
// model.safetensors and tokenizer.json and nothing else.
func csmDownloadFilters() []string {
	return []string{
		"--exclude", "transformers-*",
		"--exclude", "transformers.safetensors.index.json",
		"--exclude", "ckpt.pt",
		"--exclude", "*.gguf",
	}
}

// enoughDiskFor refuses an operation the disk cannot hold, and says the numbers.
//
// Reported rather than silently skipped, and reported BEFORE the work starts:
// the alternative is what actually happened — a twenty-minute compile ending in
// "No space left on device" repeated across a dozen crates, followed by a
// download and a config save failing for the same reason, with nothing on
// screen connecting them.
//
// A filesystem that cannot be measured is allowed through. Guessing that a
// disk is too small on a host where Statfs failed would block a machine that is
// probably fine, and the operation's own error is the fallback.
func enoughDiskFor(what, path string, need uint64) bool {
	free, ok := freeBytes(path)
	if !ok {
		return true // unmeasurable; let the operation speak for itself
	}
	if free >= need {
		return true
	}
	fmt.Println(shell.Step(shell.StateBad, "disk",
		fmt.Sprintf("not enough room for %s", what)))
	for _, l := range shell.StepDetail(fmt.Sprintf(
		"needs about %s, and %s is free. Nothing has been started — a build that "+
			"runs out partway wastes the time as well as the space.",
		humanBytes(need), humanBytes(free)), shell.Muted) {
		fmt.Println(l)
	}
	for _, l := range shell.StepDetail(
		"`/purge` reclaims Helix's own downloads, and the Hugging Face cache "+
			"lives in ~/.cache/huggingface.", shell.Muted) {
		fmt.Println(l)
	}
	return false
}

// humanBytes renders a size the way a person reads one.
func humanBytes(n uint64) string {
	const unit = 1 << 10
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit && exp < 3; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

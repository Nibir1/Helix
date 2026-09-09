// cmd/helix/python_pick.go
// Purpose: choose a Python interpreter by what it can DO, not by what `python3`
// happens to point at.
//
// WHY. `findFirstBinary` answers a PATH question, which is the right question
// for `whisper-server` and the wrong one for an interpreter: `python3` is
// always found and is not always usable. A live session on an Intel MacBook hit
// exactly that — `python3` was 3.14, onnxruntime publishes no macOS x86_64
// wheel for cp314, and piper-local failed even though a working interpreter may
// have been sitting on the same machine under another name. Helix never looked.
//
// THE ORDER MATTERS, and it is deliberate:
//
//  1. An interpreter that ALREADY imports `piper.http_server`. Offline, one
//     subprocess, and after an install it is the same interpreter that did the
//     installing — so a restart re-finds it with no network and no guessing.
//  2. The interpreter the user's PATH chose, if it can install. Helix does not
//     move someone off their default Python to satisfy its own preference.
//  3. Any other interpreter on the host that can install.
//
// Step 1 before step 2 is what makes this stable across sessions. Step 2 before
// step 3 is what keeps it from being opinionated about someone else's machine.
//
// WHAT THIS DOES NOT DO: install a different Python version. Which version to
// install is a moving upstream fact (docs/local_runtimes.md §3.7 carries the
// measured table, dated, precisely because it moves), and putting a second
// system-wide interpreter on someone's machine to satisfy a TTS voice is a
// bigger decision than a voice is worth. When nothing works, Helix says which
// interpreters it tried, what each one is, and where the version table is.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"helix/internal/speech"
)

// pythonNames are the interpreter names to look for, in the order step 2 and 3
// above require: the user's default first, then versioned names.
//
// Versioned names are listed newest-first among the ones that exist, but note
// that "newest" is NOT the same as "most likely to work" — that is the whole
// lesson of the Intel-macOS case, where the newest interpreter is the one
// without wheels. The probe decides; this list only decides the order in which
// candidates are asked.
func pythonNames() []string {
	names := []string{"python3", "python"}
	// 3.14 down to 3.9. The floor is piper-tts's own abi3 tag (cp39), below
	// which its wheels do not apply at all.
	for minor := 14; minor >= 9; minor-- {
		names = append(names, "python3."+itoa(minor))
	}
	return names
}

// itoa avoids a strconv import for two digits.
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// pythonSearchDirs are the places an interpreter hides when it is not on PATH.
//
// Not speculative: a Homebrew python on an Intel Mac lives under /usr/local,
// on Apple Silicon under /opt/homebrew, and python.org framework builds put
// theirs somewhere PATH often misses entirely. A user who installed 3.13 to fix
// this and did not re-shell would otherwise still be told no.
func pythonSearchDirs() []string {
	dirs := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
	}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs,
			filepath.Join(home, ".pyenv", "shims"),
			filepath.Join(home, ".local", "bin"),
		)
	}
	// python.org framework builds: /Library/Frameworks/Python.framework/Versions/3.13/bin
	if entries, err := os.ReadDir("/Library/Frameworks/Python.framework/Versions"); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs,
					filepath.Join("/Library/Frameworks/Python.framework/Versions", e.Name(), "bin"))
			}
		}
	}
	return dirs
}

// discoverPythons returns every distinct interpreter on this host.
//
// Deduplicated by IDENTITY rather than by path, because `python3`,
// `python3.14` and a Homebrew symlink are routinely the same interpreter and
// probing one three times would cost three index round trips to learn one
// thing. Identity comes from the interpreter itself (version + wheel platform
// tag), which is also what makes the dedup correct for a universal2 build.
func discoverPythons() []pythonCandidate {
	var found []pythonCandidate
	seen := map[string]bool{}

	add := func(path string) {
		if path == "" {
			return
		}
		id := pythonIdentity(path)
		if id == "" {
			return // not an interpreter, or it cannot answer
		}
		if seen[id] {
			return
		}
		seen[id] = true
		found = append(found, pythonCandidate{Path: path, Identity: id})
	}

	for _, name := range pythonNames() {
		if p, err := exec.LookPath(name); err == nil {
			add(p)
		}
	}
	for _, dir := range pythonSearchDirs() {
		for _, name := range pythonNames() {
			p := filepath.Join(dir, name)
			if isRunnableFile(p) {
				add(p)
			}
		}
	}
	return found
}

// pythonCandidate is one interpreter and what it says it is.
type pythonCandidate struct {
	Path     string
	Identity string // "Python 3.13 on macosx-10.9-x86_64"
}

// pickPythonForPiper chooses the interpreter piper-local should use.
//
// Returns the path, a one-line explanation of the choice, and whether one was
// found. The explanation is returned rather than printed so the caller decides
// whether this is a step, a status row or a refusal.
//
// Memoized: every branch costs at least one subprocess and step 3 costs an
// index round trip per candidate.
func pickPythonForPiper() (string, string, bool) {
	if pythonPickDone {
		return pythonPickPath, pythonPickWhy, pythonPickOK
	}
	path, why, ok := choosePythonForPiper()
	pythonPickPath, pythonPickWhy, pythonPickOK, pythonPickDone = path, why, ok, true
	return path, why, ok
}

var (
	pythonPickDone bool
	pythonPickOK   bool
	pythonPickPath string
	pythonPickWhy  string
)

// resetPythonPick clears the memo. Tests only.
func resetPythonPick() {
	pythonPickDone, pythonPickOK, pythonPickPath, pythonPickWhy = false, false, "", ""
}

func choosePythonForPiper() (string, string, bool) {
	candidates := discoverPythons()
	if len(candidates) == 0 {
		return "", "no Python interpreter found on this host", false
	}

	// 1. Already serving. Offline, and stable across restarts.
	for _, c := range candidates {
		if pythonHasPiper(c.Path) {
			return c.Path, c.Identity + " already has piper", true
		}
	}

	// 2. The user's own default, if it can install. Checked before the others
	//    so Helix does not relocate someone's Python for its own convenience.
	for _, c := range candidates {
		if base := filepath.Base(c.Path); base != "python3" && base != "python" {
			continue
		}
		if _, blocked, conclusive := piperInstallVerdict(c.Path); !blocked {
			return c.Path, c.Identity + " " + canInstallPhrase(conclusive), true
		}
	}

	// 3. Anything else that can.
	for _, c := range candidates {
		if _, blocked, conclusive := piperInstallVerdict(c.Path); !blocked {
			return c.Path, c.Identity + " " + canInstallPhrase(conclusive) +
				" (your default python cannot)", true
		}
	}

	return "", noUsablePythonReason(candidates), false
}

// pythonHasPiper reports whether an interpreter can already import the SERVER
// module.
//
// The server module, not the package root: piper-tts installs fine while
// `piper.http_server` still fails, because the HTTP server imports flask and
// flask is not one of its dependencies. Asking the root would call a
// configuration ready that dies on startup — the same distinction the spec's
// Verify draws, and deliberately the same check.
func pythonHasPiper(python string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, python, "-c", "import piper.http_server").Run() == nil //nolint:gosec // interpreter from discovery
}

// noUsablePythonReason explains a refusal by naming what was actually tried.
//
// "no Python with wheels" sends nobody anywhere. Listing the interpreters and
// what each one IS turns it into a decision the user can make: they can see
// that the only interpreter present is the one without wheels, and the dated
// table says which versions have them.
func noUsablePythonReason(candidates []pythonCandidate) string {
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.Identity)
	}
	sort.Strings(ids)
	return "no interpreter on this host can install piper-tts. Tried: " +
		strings.Join(ids, "; ") + ". docs/local_runtimes.md §3.7 lists which " +
		"interpreters have wheels for this platform — installing one " +
		"(brew install python@<version>, or your distribution's python3.<version> " +
		"package) is the fix, and /blackbox setup offers every alternative voice."
}

// choosePiperBinary is piper-local's answer to "which executable serves".
//
// The native standalone binary wins whenever it is present: it needs no
// interpreter, no modules and no server, which is the whole reason Helix ships
// it. Only when there is none does this become a Python question, and only then
// is anything probed.
func choosePiperBinary() (string, string, bool) {
	if bin, ok := piperBinaryReady(); ok {
		return bin, piperReadyWhy, true
	}
	return pickPythonForPiper()
}

// piperReadyWhy explains the answer piperBinaryReady just gave. Set alongside
// it because the two are read together and a second lookup would be a second
// chance to disagree.
var piperReadyWhy string

// piperBinaryReady answers the same question OFFLINE: is something already
// able to serve piper on this host?
//
// This exists because the answer is needed on two very different paths. The
// install flow may spend seconds asking pip which interpreter could work;
// RENDERING a status row or a launch command may not spend anything, and must
// never reach the network. So the cheap half — the native binary, or an
// interpreter that already imports the server module — is available on its own.
//
// launchCommandFor is the caller that matters here. Its own comment requires
// that it resolve the binary "the same way the launcher resolves it", and after
// the launcher grew a capability-aware Choose, a plain PATH lookup there would
// have drifted straight back into the bug that comment was written for: a
// command rendered for `piper` while a Python interpreter is what actually runs.
func piperBinaryReady() (string, bool) {
	if bin, err := speech.FindPiperBinary(); err == nil {
		piperReadyWhy = "standalone piper binary (no Python needed)"
		return bin, true
	}
	for _, c := range discoverPythons() {
		if pythonHasPiper(c.Path) {
			piperReadyWhy = c.Identity + " already has piper"
			return c.Path, true
		}
	}
	return "", false
}

// piperOrPathBinary resolves a sidecar's executable for RENDERING: capability
// for piper, PATH order for everything else, and no network either way.
func piperOrPathBinary(sc voiceSidecar) (string, bool) {
	if sc.Choose != nil {
		if bin, ok := piperBinaryReady(); ok {
			return bin, true
		}
		// Nothing is serving yet, so fall THROUGH to the PATH order rather
		// than reporting nothing. Returning false here was a real regression,
		// caught by TestLaunchCommandNeverPairsNativePiperWithPythonArgs: the
		// caller's fallback is Binaries[0], which for piper is "piper", and
		// pairing that with `-m piper.http_server` renders a command only an
		// interpreter could run. An interpreter that exists but has no module
		// yet is still the right thing to name — the install step is about to
		// give it one.
	}
	return findFirstBinary(sc.Binaries)
}

// canInstallPhrase says which of two things Helix actually knows.
//
// A conclusive probe means pip resolved the install as wheels-only and said
// yes. An inconclusive one means pip could not be asked — a pip too old for
// --dry-run, no network, a proxy — and the honest report is that this is the
// interpreter Helix will TRY, not one it has verified. The first version of
// this said "can install piper" for both, which on the machine it was written
// on was a claim about a pip that had refused to answer.
func canInstallPhrase(conclusive bool) string {
	if conclusive {
		return "can install piper"
	}
	return "is the interpreter Helix will use (pip could not be asked in advance)"
}

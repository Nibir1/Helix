// cmd/helix/python_pick_test.go
// Purpose: pin the interpreter choice — its ORDER, its dedup, and the two
// places it must never reach the network.
//
// The order is the whole design, and each step exists for a measured reason:
// step 1 (already has the module) keeps the answer stable across restarts with
// no network; step 2 (the user's own `python3`) stops Helix relocating someone
// off their default Python for its own convenience; step 3 is the case that
// started this — an Intel MacBook whose `python3` was 3.14, which onnxruntime
// publishes no macOS x86_64 wheel for, while another interpreter may have been
// sitting on the same machine.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Discovery must deduplicate by IDENTITY, not by path.
//
// `python3`, `python3.14` and a Homebrew symlink are routinely one interpreter.
// Deduplicating by path would probe it three times, and each probe in step 3 is
// an index round trip — three network calls to learn one fact.
func TestDiscoverPythonsDeduplicatesByIdentity(t *testing.T) {
	found := discoverPythons()
	if len(found) == 0 {
		t.Skip("no interpreter on this host to discover")
	}
	seen := map[string]string{}
	for _, c := range found {
		if c.Identity == "" {
			t.Errorf("candidate %s has no identity — it would defeat the dedup", c.Path)
			continue
		}
		if prev, dup := seen[c.Identity]; dup {
			t.Errorf("two candidates share identity %q (%s and %s) — the dedup is not working, "+
				"and each duplicate costs a pip resolution", c.Identity, prev, c.Path)
		}
		seen[c.Identity] = c.Path
	}
	for _, c := range found {
		if !strings.HasPrefix(c.Identity, "Python 3.") {
			t.Errorf("identity %q should lead with the version", c.Identity)
		}
	}
}

// The search must look beyond PATH, because the fix for this whole problem is
// "install a 3.13" and a user who does that without re-shelling would
// otherwise still be told no.
func TestPythonSearchCoversOffPathInstallLocations(t *testing.T) {
	dirs := pythonSearchDirs()
	want := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	if runtime.GOOS != "darwin" {
		want = []string{"/usr/local/bin"}
	}
	for _, w := range want {
		if !contains(dirs, w) {
			t.Errorf("search dirs %v missing %s — a Homebrew interpreter lives there", dirs, w)
		}
	}
	// pyenv shims and --user installs are the other two that routinely miss PATH.
	if home, err := os.UserHomeDir(); err == nil {
		for _, w := range []string{
			filepath.Join(home, ".pyenv", "shims"),
			filepath.Join(home, ".local", "bin"),
		} {
			if !contains(dirs, w) {
				t.Errorf("search dirs missing %s", w)
			}
		}
	}
}

// The candidate NAMES must span the versions piper-tts can actually run on,
// and must ask the user's default first.
func TestPythonNamesAskTheDefaultFirstThenVersions(t *testing.T) {
	names := pythonNames()
	if len(names) < 3 || names[0] != "python3" {
		t.Fatalf("pythonNames() = %v, want python3 first — Helix must not move someone off "+
			"their default interpreter to satisfy its own preference", names)
	}
	// cp39 is piper-tts's own abi3 floor; below it the wheels do not apply.
	for _, want := range []string{"python3.13", "python3.9"} {
		if !contains(names, want) {
			t.Errorf("pythonNames() = %v, missing %s", names, want)
		}
	}
	if contains(names, "python3.8") {
		t.Error("3.8 is below piper-tts's cp39 abi3 floor; probing it would spend a network " +
			"round trip to learn something the wheel tag already says")
	}
}

// itoa is hand-rolled for two digits; a wrong digit would silently look for
// interpreters that do not exist.
func TestItoaTwoDigits(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{{9, "9"}, {10, "10"}, {13, "13"}, {14, "14"}} {
		if got := itoa(tc.in); got != tc.want {
			t.Errorf("itoa(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The RENDER path must never probe.
//
// launchCommandFor is called from status output. Its own comment requires it to
// resolve the binary the way the launcher does, and after the launcher grew a
// capability-aware Choose, satisfying that comment with a network probe would
// have made a status report hang.
func TestRenderPathUsesNoNetwork(t *testing.T) {
	src, err := os.ReadFile("python_pick.go")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(string(src), "func piperBinaryReady(")
	if body == "" {
		t.Fatal("could not find piperBinaryReady — the test cannot reach what it checks")
	}
	body = stripLineComments(body)
	for _, probe := range []string{"piperInstallBlocked", "piperProbeAsk", "pickPythonForPiper"} {
		if strings.Contains(body, probe) {
			t.Errorf("piperBinaryReady calls %s — that reaches the package index, and this "+
				"function is on the status-rendering path", probe)
		}
	}
	if !strings.Contains(body, "pythonHasPiper") {
		t.Error("the offline check must still ask whether an interpreter can import the server")
	}
}

// ...and the render path must fall THROUGH to the PATH order when nothing is
// serving yet, which is the regression the existing launch-command guard
// caught: returning "not found" made the caller name Binaries[0] ("piper") and
// pair it with `-m piper.http_server`, a command only an interpreter can run.
func TestRenderPathFallsBackToPathOrder(t *testing.T) {
	spec, ok := voiceSidecars()["piper-local"]
	if !ok {
		t.Fatal("piper-local is not in the sidecar table")
	}
	if spec.Choose == nil {
		t.Fatal("piper-local must select by capability, or this test is checking nothing")
	}
	bin, found := piperOrPathBinary(spec)
	if !found {
		// Legitimate on a host with no piper AND no interpreter at all.
		if _, hasPython := findFirstBinary([]string{"python3", "python"}); hasPython {
			t.Error("an interpreter exists on PATH and the render path reported nothing — " +
				"the caller would then name \"piper\" and pair it with Python arguments")
		}
		return
	}
	if bin == "" {
		t.Error("reported found with an empty path")
	}
}

// A refusal must name what was tried. "No Python with wheels" sends nobody
// anywhere; the interpreters and their identities turn it into a decision.
func TestNoUsablePythonReasonNamesWhatWasTried(t *testing.T) {
	got := noUsablePythonReason([]pythonCandidate{
		{Path: "/usr/bin/python3", Identity: "Python 3.14 on macosx-10.9-x86_64"},
		{Path: "/opt/homebrew/bin/python3.9", Identity: "Python 3.9 on macosx-10.9-x86_64"},
	})
	for _, want := range []string{"Python 3.14", "Python 3.9", "local_runtimes.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal = %q, want it to mention %q", got, want)
		}
	}
	// And it must not invent a version to install — that table moves upstream.
	if strings.Contains(got, "3.13") {
		t.Error("the refusal names a specific version to install; that is the rotting matrix " +
			"the dated doc table exists to hold")
	}
}

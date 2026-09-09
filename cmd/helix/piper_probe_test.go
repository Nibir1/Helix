// cmd/helix/piper_probe_test.go
// Purpose: the piper-local Python refusal — its parsing, and above all that it
// fails OPEN.
//
// From a real session on 2026-09-09 (Intel Mac, Python 3.14): `pip install
// piper-tts flask` spent minutes backtracking through eleven piper-tts
// releases, building metadata from an sdist, and then failed — onnxruntime and
// piper-phonemize publish no wheel for darwin/amd64 on cp314 and there is no
// source build. The user watched sixty lines of "Using cached" to be told no.
package main

import (
	"os"
	"strings"
	"testing"
)

// The refusal must name the packages that are actually missing, because
// "a dependency is unavailable" sends nobody anywhere.
func TestMissingWheelNamesReadsPipsOwnWords(t *testing.T) {
	// Trimmed from the session's real output.
	out := `ERROR: Cannot install piper-tts==1.1.0, piper-tts==1.8.0 because these
package versions have conflicting dependencies.
Additionally, some packages in these conflicts have no matching distributions
available for your environment:
    onnxruntime
    piper-phonemize
`
	got := missingWheelNames(out)
	if len(got) < 2 {
		t.Fatalf("missingWheelNames = %v, want both blockers named", got)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"onnxruntime", "piper-phonemize"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missingWheelNames = %v, want it to name %s", got, want)
		}
	}
}

// A refusal that cannot say why must still say something actionable rather
// than an empty list.
func TestMissingWheelNamesFallsBackToTheUsualCause(t *testing.T) {
	got := missingWheelNames("ERROR: something else entirely")
	if len(got) != 1 || got[0] != "onnxruntime" {
		t.Errorf("missingWheelNames = %v, want the usual blocker named", got)
	}
}

// The probe must be MEMOIZED. It spawns a subprocess and reaches the package
// index, the wizard can ask more than once in a run, and the answer cannot
// change while Helix is running.
func TestPiperProbeIsAskedAtMostOnce(t *testing.T) {
	savedDone, savedBlocked, savedReason :=
		piperProbeDone, piperProbeBlocked, piperProbeReason
	t.Cleanup(func() {
		piperProbeDone, piperProbeBlocked, piperProbeReason =
			savedDone, savedBlocked, savedReason
	})

	piperProbeDone, piperProbeBlocked, piperProbeReason = true, true, "cached verdict"
	reason, blocked := piperPythonBlocked()
	if !blocked || reason != "cached verdict" {
		t.Fatalf("piperPythonBlocked() = (%q, %v) — a completed probe must be reused, "+
			"not repeated; each run is a subprocess and an index round trip",
			reason, blocked)
	}
}

// The load-bearing property: an inconclusive probe must NOT block a path that
// might work.
//
// Measured on this machine, which is why the rule exists: the Xcode
// CommandLineTools python3 ships a pip with no `--dry-run`, so the probe exits
// non-zero having decided nothing. A verdict from that would refuse
// piper-local on a host where it may install perfectly well. No network, an
// http proxy and a corporate index do the same thing.
func TestPiperProbeFailsOpenOnAnUnusableProbe(t *testing.T) {
	savedDone, savedBlocked, savedReason :=
		piperProbeDone, piperProbeBlocked, piperProbeReason
	t.Cleanup(func() {
		piperProbeDone, piperProbeBlocked, piperProbeReason =
			savedDone, savedBlocked, savedReason
	})
	piperProbeDone = false

	// A pip too old for the flag: non-zero exit, usage text, no verdict.
	reason, blocked := piperProbeAsk("/usr/bin/false")
	if blocked {
		t.Errorf("a probe that could not run returned a verdict (%q) — an unusable probe "+
			"must never refuse a path that might work", reason)
	}
}

// ...and a probe that DID answer must be believed.
func TestPiperProbeBelievesAConclusiveAnswer(t *testing.T) {
	for _, phrase := range []string{
		"ERROR: No matching distribution found for onnxruntime",
		"ERROR: Could not find a version that satisfies the requirement onnxruntime",
		"ERROR: ResolutionImpossible: for help visit https://pip.pypa.io/",
	} {
		if !conclusivePipRefusal(phrase) {
			t.Errorf("pip said %q and the probe did not recognise it as a refusal", phrase)
		}
	}
	for _, phrase := range []string{
		"WARNING: You are using pip version 20",
		"error: subprocess-exited-with-error",
		"",
	} {
		if conclusivePipRefusal(phrase) {
			t.Errorf("%q is not a resolution failure and must not be read as one", phrase)
		}
	}
}

// The refusal must name the interpreter it is refusing FOR.
//
// "a Python with wheels, or a different voice, both work" was the first
// wording and it sent nobody anywhere: on a machine with several interpreters
// it does not even say which one was tried. The identity is asked of the
// interpreter rather than derived from Helix's own build, because a
// universal2 or Rosetta Python can report a different platform tag than
// runtime.GOARCH — which is the exact case that produces this refusal.
func TestPythonIdentityNamesVersionAndWheelTag(t *testing.T) {
	python, ok := findFirstBinary([]string{"python3", "python"})
	if !ok {
		t.Skip("no interpreter on this host to ask")
	}
	id := pythonIdentity(python)
	if id == "" {
		t.Skip("interpreter did not answer; the caller falls back to \"this Python\"")
	}
	if !strings.HasPrefix(id, "Python 3.") {
		t.Errorf("pythonIdentity = %q, want it to lead with the version", id)
	}
	// sysconfig.get_platform() is the tag pip matches wheels against, so it is
	// the half that explains WHY there is no wheel.
	if !strings.Contains(id, " on ") || strings.HasSuffix(id, " on ") {
		t.Errorf("pythonIdentity = %q, want the wheel platform tag after the version", id)
	}
}

// An interpreter that cannot answer must not produce a half-built sentence.
func TestPythonIdentityIsEmptyWhenItCannotAsk(t *testing.T) {
	if got := pythonIdentity("/usr/bin/false"); got != "" {
		t.Errorf("pythonIdentity of a non-interpreter = %q, want empty so the caller can "+
			"fall back to wording that is vaguer but never wrong", got)
	}
}

// The refusal must not compile a wheel matrix into Go. ADR-006's lesson: the
// numbers move (onnxruntime dropped Intel macOS between 1.23.2 and 1.25.0),
// and a table in source is wrong within a release. It belongs in the dated
// section of docs/local_runtimes.md, which the message points at.
func TestRefusalCarriesNoWheelMatrix(t *testing.T) {
	src, err := os.ReadFile("piper_install.go")
	if err != nil {
		t.Fatal(err)
	}
	body := functionBody(string(src), "func piperProbeAsk(")
	if body == "" {
		t.Fatal("could not find piperProbeAsk — the test cannot reach what it checks")
	}
	// Comments are stripped first. The rule is about what Helix PRINTS, not
	// about what it may explain: the comment above the message names the exact
	// numbers as the thing not to hardcode, and a guard that forbade saying so
	// would forbid the reasoning along with the mistake. (This test failed on
	// its own comment first, which is how the distinction got drawn.)
	body = stripLineComments(body)
	for _, rot := range []string{"cp310", "cp313", "cp314", "1.23.2", "3.13"} { // matrix tokens

		if strings.Contains(body, rot) {
			t.Errorf("the refusal names %q — a version matrix in Go source is wrong within "+
				"a release. Report what is true HERE and point at the dated table.", rot)
		}
	}
	if !strings.Contains(body, "local_runtimes.md") {
		t.Error("the refusal must point somewhere a reader can find which interpreters work")
	}
}

// stripLineComments removes // comments so a source-level guard judges code.
func stripLineComments(src string) string {
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

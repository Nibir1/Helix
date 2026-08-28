// cmd/helix/install_diagnosis_test.go
//
// Purpose: an installer's failure is only useful if somebody reads it.
//
// The case these pin came from a live session on an Intel Mac: pip printed
// sixty lines of candidate versions, ended in ResolutionImpossible because
// onnxruntime publishes no macOS x86_64 wheel for that Python, and Helix said
// "failed exit status 1". True, useless, and it blames the wrong layer — the
// user is left believing Helix is broken when the answer is "this interpreter
// cannot have this package".
package main

import (
	"strings"
	"testing"

	"helix/internal/shell"
)

// The real pip output, trimmed to the shape that matters.
const pipResolutionFailure = `Collecting piper-tts
  Using cached piper_tts-1.7.0-cp39-abi3-macosx_10_9_x86_64.whl.metadata (2.5 kB)
ERROR: Cannot install piper-tts==1.1.0, piper-tts==1.2.0 and piper-tts==1.7.0 because these package versions have conflicting dependencies.

The conflict is caused by:
    piper-tts 1.7.0 depends on onnxruntime<2 and >=1
    piper-tts 1.2.0 depends on piper-phonemize~=1.1.0

Additionally, some packages in these conflicts have no matching distributions available for your environment:
    onnxruntime
    piper-phonemize

To fix this you could try to:
1. loosen the range of package versions you've specified

ERROR: ResolutionImpossible: for help visit https://pip.pypa.io/en/latest/topics/dependency-resolution/
`

func TestInstallFailureNamesTheRealCauseAndAWayOut(t *testing.T) {
	lines := diagnoseInstallFailure("python3 -m pip install --user piper-tts flask",
		pipResolutionFailure)
	if len(lines) == 0 {
		t.Fatal("a resolution failure must be diagnosed, not left as \"exit status 1\"")
	}
	plain := shell.Plain(strings.Join(lines, "\n"))

	// It names the packages pip named, rather than reprinting sixty lines.
	for _, want := range []string{"onnxruntime", "piper-phonemize"} {
		if !strings.Contains(plain, want) {
			t.Errorf("diagnosis does not name %q:\n%s", want, plain)
		}
	}
	// And it offers a way out, because "not installable" with no alternative is
	// a dead end on the one screen a new user cannot skip.
	if !strings.Contains(plain, "/blackbox setup") {
		t.Errorf("diagnosis offers no alternative:\n%s", plain)
	}
	// Every line belongs to the panel.
	for _, l := range lines {
		if !strings.HasPrefix(shell.Plain(l), "  │ ") {
			t.Errorf("diagnosis line escaped the panel: %q", shell.Plain(l))
		}
	}
}

func TestMissingDistributionsReadsPipsOwnSummary(t *testing.T) {
	got := missingDistributions(pipResolutionFailure)
	if len(got) != 2 || got[0] != "onnxruntime" || got[1] != "piper-phonemize" {
		t.Errorf("got %v, want [onnxruntime piper-phonemize]", got)
	}
	// The block ends at the first non-indented line — it must not swallow the
	// numbered advice that follows.
	for _, name := range got {
		if strings.Contains(name, "loosen") || strings.Contains(name, "1.") {
			t.Errorf("ran past the end of the block: %v", got)
		}
	}
	if n := missingDistributions("nothing relevant here"); n != nil {
		t.Errorf("unrelated output produced %v", n)
	}
}

// PEP 668 is the other failure people actually hit, on Debian and Ubuntu.
func TestExternallyManagedEnvironmentIsRecognised(t *testing.T) {
	lines := diagnoseInstallFailure("python3 -m pip install --user x",
		"error: externally-managed-environment\n× This environment is externally managed")
	if len(lines) == 0 {
		t.Fatal("PEP 668 must be diagnosed — it is not a Helix failure")
	}
	if !strings.Contains(shell.Plain(strings.Join(lines, "\n")), "PEP 668") {
		t.Error("the diagnosis should name the rule that blocked it")
	}
}

// An unrecognised failure produces nothing rather than a guess. The command's
// own output is already on screen; inventing a cause would be worse than
// silence.
func TestUnrecognisedFailureIsNotGuessedAt(t *testing.T) {
	if lines := diagnoseInstallFailure("brew install x", "something else went wrong"); len(lines) != 0 {
		t.Errorf("expected no diagnosis, got %d lines", len(lines))
	}
}

// The tee must not grow without bound: it captures a subprocess Helix does not
// control, and "however much it prints" is not a size.
func TestCapturedOutputIsBounded(t *testing.T) {
	var b boundedLog
	chunk := strings.Repeat("x", 8192)
	for i := 0; i < 64; i++ { // 512 KiB in
		if _, err := b.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(b.String()); got > boundedLogMax {
		t.Errorf("captured %d bytes, over the %d cap", got, boundedLogMax)
	}
	// And it keeps the TAIL, where pip's one useful sentence lives.
	_, _ = b.Write([]byte("THE-LAST-LINE"))
	if !strings.HasSuffix(b.String(), "THE-LAST-LINE") {
		t.Error("the bounded log must keep the end, not the beginning")
	}
}

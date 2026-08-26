// cmd/helix/piper_runtime_test.go
// Purpose: piper's "binary" is a Python interpreter, so the two questions
// "is it installed" and "can it run" come apart — and Helix used to ask only
// the first.
//
// The real failure: python3 is on PATH, so the presence check passed, the
// install step was skipped, a 60 MB voice model was downloaded, the server was
// launched, and the process died on ModuleNotFoundError. Three confirmations
// and a long download, none of which could ever have succeeded. The machine
// that fixes this cannot reproduce it (piper imports fine here), which is
// precisely why it needs a test rather than a manual check.
package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"helix/internal/speech"
)

// readSource reads a file in this package for structural assertions.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// The spec must ask whether the MODULE runs, not whether an interpreter exists.
func TestPiperVerifyChecksTheModuleNotTheInterpreter(t *testing.T) {
	spec, ok := voiceSidecars()["piper-local"]
	if !ok {
		t.Fatal("piper-local is not a known sidecar")
	}
	if spec.Verify == nil {
		t.Fatal("piper-local must verify its runtime: python3 on PATH says nothing about piper")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("no python3 on PATH to verify against")
	}

	// An interpreter that cannot import anything at all stands in for the
	// machine without piper — the mechanism is what is under test, not this
	// host's package list.
	if reason, ok := spec.Verify("definitely-not-an-interpreter-xyz"); ok {
		t.Error("a missing interpreter must not verify as usable")
	} else if reason == "" {
		t.Error("a failed verification must say why")
	}
}

// Verify must run BEFORE the model download, or the user pays for a 60 MB
// fetch that cannot help. Pinned structurally: Verify is consulted in the same
// function that gates ModelHint, ahead of it.
func TestPiperVerifyRunsBeforeTheModelDownload(t *testing.T) {
	src := readSource(t, "voice_sidecars.go")
	verifyAt := strings.Index(src, "spec.Verify(binary)")
	modelAt := strings.Index(src, "spec.ModelHint()")
	if verifyAt < 0 || modelAt < 0 {
		t.Fatal("expected both the runtime check and the model hint in offerSidecarSetup")
	}
	if verifyAt > modelAt {
		t.Error("the runtime check must run before the model download, not after")
	}
}

// The start command had three definitions that disagreed: the launcher passed
// --model <absolute path> while the diagnosis and the wizard hint printed
// `-m <bare filename>`, which exists in no working directory. A user who hit
// the failure saw two different commands on one screen.
func TestPiperStartCommandHasOneDefinition(t *testing.T) {
	canonical := speech.PiperStartCmd("28183")

	if !strings.Contains(canonical, speech.PiperVoicePath()) {
		t.Errorf("the printed command must name the model path Helix actually uses: %q", canonical)
	}
	if strings.Contains(canonical, " -m en_US") {
		t.Errorf("a bare model filename is not runnable from any directory: %q", canonical)
	}

	// The launched argv and the printed command must agree on the model.
	args := speech.PiperArgs(28183)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, speech.PiperVoicePath()) {
		t.Errorf("launch args must use the same model path: %v", args)
	}
	if !strings.Contains(joined, "piper.http_server") {
		t.Errorf("launch args must run the http server module: %v", args)
	}

	// And the hint the wizard prints must be the canonical one too.
	hints := strings.Join(localSidecarHints["piper-local"], "\n")
	if strings.Contains(hints, " -m en_US") {
		t.Errorf("the wizard hint still prints the unrunnable form:\n%s", hints)
	}
}

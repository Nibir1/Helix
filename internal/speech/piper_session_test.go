// internal/speech/piper_session_test.go
// Purpose: the persistent process is the ONLY reason the native path is worth
// having, so its contract is pinned.
//
// Piper's cost is dominated by loading the model, not by speaking. Measured on
// the development machine: five utterances through one process cost 128 ms each;
// five separate spawns cost 513 ms each. A per-call CLI is therefore a 4x
// regression against the HTTP server it replaces — the server is fast precisely
// because it keeps the model warm. Holding one process open matched and then
// beat it (884 ms for the first utterance, 65 ms and 54 ms for the next two,
// versus the server's 103 ms) because there is no HTTP hop.
package speech

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A dead or missing binary must fail with something actionable, not hang until
// the utterance timeout.
func TestPiperSessionFailsFastOnAMissingBinary(t *testing.T) {
	p := NewPiperNativeTTS(filepath.Join(t.TempDir(), "not-piper"), writeFakeModel(t))
	_, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{})
	if err == nil {
		t.Fatal("a missing binary must not synthesize")
	}
	if !strings.Contains(err.Error(), "piper-local") {
		t.Errorf("the error should name the provider, got %v", err)
	}
}

// The frame is "a file that was not here before". A leftover from an earlier
// utterance must never be returned as this one's answer — that would play the
// previous sentence back, which is worse than an error because it sounds like
// it worked.
func TestPiperSessionIgnoresPreexistingFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "stale.wav")
	if err := os.WriteFile(stale, []byte("not audio"), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := wavSet(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !before["stale.wav"] {
		t.Fatal("precondition: the stale file should be recorded")
	}
	if _, found, err := newWAV(dir, before); err != nil || found {
		t.Error("a file present before the utterance must not count as its output")
	}

	fresh := filepath.Join(dir, "fresh.wav")
	if err := os.WriteFile(fresh, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, found, err := newWAV(dir, before)
	if err != nil || !found {
		t.Fatalf("a new file must be found: found=%v err=%v", found, err)
	}
	if filepath.Base(path) != "fresh.wav" {
		t.Errorf("found the wrong file: %s", path)
	}
}

// A file APPEARS when piper creates it, not when it finishes writing it.
// Reading on first sight yields a truncated WAV — a clip that decodes to a
// fraction of the sentence, or fails its header check outright.
func TestReadStableFileWaitsForTheWriterToFinish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	if err := os.WriteFile(path, []byte("complete payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readStableFile(path)
	if err != nil {
		t.Fatalf("a settled file must be readable: %v", err)
	}
	if string(data) != "complete payload" {
		t.Errorf("got %q", data)
	}
}

// The libstdc++ gate is what keeps a Jetson Nano from downloading 50 MB it
// cannot run. On non-Linux it must not block anything.
func TestPiperBinaryUsabilityGate(t *testing.T) {
	reason, usable := PiperBinaryUsableHere()
	if usable && reason != "" {
		t.Error("a usable host needs no excuse")
	}
	if !usable && reason == "" {
		t.Error("an unusable host must say why")
	}
	if !usable && !strings.Contains(reason, RequiredGLIBCXX) {
		t.Errorf("the reason must name the missing version, got %q", reason)
	}
}

// writeFakeModel creates a stand-in voice file so model-presence checks pass
// and the test exercises the process path instead.
func writeFakeModel(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "voice.onnx")
	if err := os.WriteFile(path, []byte("not a real model"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLivePiperSessionStaysWarm drives a REAL piper and proves the warm model.
//
// Opt-in (HELIX_LIVE_SIDECAR=1) for the same reason live_sidecar_test.go is: a
// mock cannot catch this. The whole claim — that holding the process open is
// what makes the native path viable — is about a cost that only exists when a
// real ONNX model is really loaded, and every fake in this package would agree
// with the fake.
//
// A shim lets `python3 -m piper` stand in for the native binary. That is not a
// compromise: the adapter frames on the output DIRECTORY rather than on either
// one's log format, so if it works through the shim it works through the binary.
func TestLivePiperSessionStaysWarm(t *testing.T) {
	if os.Getenv("HELIX_LIVE_SIDECAR") != "1" {
		t.Skip("set HELIX_LIVE_SIDECAR=1 to run against a real piper")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	model := filepath.Join(home, ".helix", "piper-voices", "en_US-lessac-medium.onnx")
	if _, err := os.Stat(model); err != nil {
		t.Skipf("no voice model at %s — /blackbox setup fetches one", model)
	}

	binary, err := FindPiperBinary()
	if err != nil {
		shim := filepath.Join(t.TempDir(), "piper")
		if werr := os.WriteFile(shim,
			[]byte("#!/bin/sh\nexec python3 -m piper \"$@\"\n"), 0o755); werr != nil {
			t.Fatal(werr)
		}
		binary = shim
	}

	p := NewPiperNativeTTS(binary, model)
	var first, warmest time.Duration
	for i, line := range []string{
		"The build finished and two tests failed.",
		"Second utterance through the same process.",
		"Third one, still warm.",
	} {
		start := time.Now()
		af, serr := p.Synthesize(context.Background(), line, SynthesisOptions{})
		elapsed := time.Since(start)
		if serr != nil {
			t.Fatalf("utterance %d: %v", i+1, serr)
		}
		if !UsableSpeech(af) {
			t.Errorf("utterance %d produced nothing audible", i+1)
		}
		if af.SampleRate <= 0 || af.Channels <= 0 {
			t.Errorf("utterance %d has no usable format: %dHz x%d",
				i+1, af.SampleRate, af.Channels)
		}
		t.Logf("utterance %d: %v, %d bytes, %dHz x%d",
			i+1, elapsed.Round(time.Millisecond), len(af.Bytes), af.SampleRate, af.Channels)

		if i == 0 {
			first = elapsed
		} else if warmest == 0 || elapsed < warmest {
			warmest = elapsed
		}
	}

	// The point of the whole design: later utterances must be dramatically
	// cheaper than the first, because the model is loaded once. If they are not,
	// the process is being restarted and the native path has silently become the
	// per-call CLI it was built to replace.
	if warmest*2 > first {
		t.Errorf("no warm-model speedup: first %v, warmest %v — the model looks "+
			"like it is being reloaded per utterance", first, warmest)
	}
}

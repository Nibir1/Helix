// internal/speech/piper_native_shim_test.go
//
// Purpose: one name, one provider.
//
// `pip install piper-tts` puts a `piper` CONSOLE SCRIPT on PATH. Treating it as
// the standalone binary made newPiperProvider wrap a Python shim in the native
// adapter — so /blackbox setup would start the HTTP server, verify it answering
// on its port, and then speech.Status would health-check the shim instead and
// report "piper-local still not answering" three lines below "✔ verified".
// Two providers behind one name, disagreeing in the same screen of output.
package speech

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// execName gives a fixture the name PATH lookup will actually find.
//
// Windows resolves a bare `piper` through PATHEXT, so a file with no extension
// is invisible to exec.LookPath however executable its contents. Writing the
// Unix name on every platform made TestRealBinaryIsStillFound fail on
// windows-latest for a reason that had nothing to do with what it was testing.
func execName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func writeExec(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// The pip console script must not be mistaken for the native runtime.
func TestPipConsoleScriptIsNotTheNativeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		// pip does not write a shebang script on Windows — it writes a real PE
		// launcher named piper.exe, which no byte-level test can tell from the
		// standalone build. The heuristic is genuinely Unix-only, and claiming
		// otherwise here would be a green test for a property that does not
		// hold. Recorded in local_runtimes.md rather than hidden.
		t.Skip("pip installs a PE launcher on Windows, not a #! script")
	}
	dir := t.TempDir()
	shim := writeExec(t, dir, execName("piper"),
		"#!/usr/local/opt/python@3.12/bin/python3.12\n"+
			"import sys\nfrom piper.__main__ import main\nsys.exit(main())\n")

	if IsNativePiperBinary(shim) {
		t.Error("a #! script was accepted as the standalone piper binary")
	}

	home := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got, err := FindPiperBinary(); err == nil {
		t.Errorf("FindPiperBinary returned %q for a Python console script; "+
			"speech.Status would then probe the shim while the wizard verified "+
			"the HTTP server", got)
	}
}

// A shell wrapper is the same mistake in a different language.
func TestShellWrapperIsNotTheNativeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrappers with a #! line are not a Windows mechanism")
	}
	dir := t.TempDir()
	wrapper := writeExec(t, dir, execName("piper"), "#!/bin/sh\nexec python3 -m piper \"$@\"\n")
	if IsNativePiperBinary(wrapper) {
		t.Error("a /bin/sh wrapper was accepted as the standalone piper binary")
	}
}

// ...and a genuine executable must still be found, or this fix would simply
// disable the native path it exists to protect.
func TestRealBinaryIsStillFound(t *testing.T) {
	dir := t.TempDir()
	// Mach-O/ELF/PE all begin with a magic number, never "#!".
	real := writeExec(t, dir, execName("piper"), "\x7fELF\x02\x01\x01\x00native piper")

	if !IsNativePiperBinary(real) {
		t.Fatal("a real executable was rejected")
	}

	home := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got, err := FindPiperBinary()
	if err != nil {
		t.Fatalf("FindPiperBinary rejected a genuine binary: %v", err)
	}
	if got != real {
		t.Errorf("FindPiperBinary = %q, want %q", got, real)
	}
}

// An empty or truncated file is not a runtime either.
func TestTruncatedFileIsNotTheNativeBinary(t *testing.T) {
	dir := t.TempDir()
	if IsNativePiperBinary(writeExec(t, dir, execName("piper"), "")) {
		t.Error("an empty file was accepted as the standalone piper binary")
	}
}

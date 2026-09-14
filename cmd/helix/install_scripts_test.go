// cmd/helix/install_scripts_test.go
// Purpose: the install scripts cannot be unit-tested by calling them — one
// needs Windows, the other writes to /usr/local/bin — so pin the invariants
// that broke instead.
//
// Two real failures are behind this file:
//
//  1. install.sh called `sudo` unconditionally. Under MSYS2/MINGW64, where
//     `make install` is the natural thing to type, sudo does not exist: the
//     install died with "sudo: command not found", exit 127, having installed
//     nothing. Same assumption breaks a root container.
//
//  2. install.ps1 looked for dist\helix.exe, which no build target produces.
//     `make windows` writes helix-windows-amd64.exe and `make current`
//     writes helix with no suffix, so the script built successfully and then
//     failed on the copy.
package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readScript(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// Every privileged step must go through the wrapper that checks whether
// elevation is available, rather than assuming a binary that half the
// supported platforms do not ship.
func TestInstallScriptNeverCallsSudoDirectly(t *testing.T) {
	src := readScript(t, "../../scripts/install.sh")

	// A command position: start of line, after `|`, or after `&&`/`;`.
	bare := regexp.MustCompile(`(?m)(^|\||&&|;)\s*sudo\s`)
	inWrapper := false
	var sawWrapperSudo bool
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		// The wrapper's own body is the ONE place sudo belongs — it is the
		// thing that first checks whether sudo is there at all.
		if strings.HasPrefix(trimmed, "run_privileged()") {
			inWrapper = true
		} else if inWrapper && line == "}" {
			inWrapper = false
		}
		if strings.HasPrefix(trimmed, "#") || strings.Contains(trimmed, "sudo is not installed") {
			continue // prose about sudo is the point of the fix, not a call
		}
		if inWrapper {
			if bare.MatchString(line) {
				sawWrapperSudo = true
			}
			continue
		}
		if bare.MatchString(line) {
			t.Errorf("scripts/install.sh:%d calls sudo directly:\n\t%s\n"+
				"MSYS2 and root containers have no sudo; route it through run_privileged",
				i+1, trimmed)
		}
	}

	if !strings.Contains(src, "run_privileged()") {
		t.Error("scripts/install.sh has no run_privileged wrapper; nothing is deciding " +
			"whether elevation is needed or even possible")
	}
	// Without this the test passes trivially the day someone deletes the sudo
	// call from the wrapper and leaves an install that cannot elevate at all.
	if !sawWrapperSudo {
		t.Error("run_privileged never calls sudo, so nothing can elevate on a machine " +
			"where elevation IS required")
	}
}

// MSYS2 reports OSTYPE as "msys2.0" and Cygwin appends a version too, so an
// equality test against "msys" matches neither. The guard must be a prefix
// match, or the Windows branches silently never run.
func TestInstallScriptDetectsWindowsByPrefix(t *testing.T) {
	src := readScript(t, "../../scripts/install.sh")

	if regexp.MustCompile(`OSTYPE"?\s*(==|!=)\s*"?(msys|cygwin|win32)"`).MatchString(src) {
		t.Error("scripts/install.sh compares $OSTYPE for equality with a bare platform " +
			"name; MSYS2 reports msys2.0 and Cygwin reports cygwin with a version, " +
			"so the Windows branch would never be taken")
	}
	for _, want := range []string{"msys*", "cygwin*", "win32*"} {
		if !strings.Contains(src, want) {
			t.Errorf("scripts/install.sh has no %q case; that Windows environment is unhandled", want)
		}
	}
}

// A Windows install that lands a file with no extension cannot be run from cmd
// or PowerShell, because the PATH search is driven by PATHEXT.
func TestInstallScriptNamesTheWindowsBinaryExe(t *testing.T) {
	src := readScript(t, "../../scripts/install.sh")
	if !strings.Contains(src, `INSTALLED_NAME="$BINARY_NAME.exe"`) {
		t.Error("scripts/install.sh does not add .exe on Windows; `go build -o dist/helix` " +
			"produces a PE file with no extension, which Windows will not find on PATH")
	}
}

// install.ps1 must look for every name the build scripts can actually write.
// Derived from build.sh rather than hardcoded, so renaming an output there
// fails here instead of failing on a user's machine after a successful build.
func TestWindowsInstallerLooksForTheNamesBuildScriptsProduce(t *testing.T) {
	build := readScript(t, "../../scripts/build.sh")
	ps1 := readScript(t, "../../scripts/install.ps1")

	outputs := regexp.MustCompile(`go build -o "\$DIST_DIR/([^"]+)"`).FindAllStringSubmatch(build, -1)
	if len(outputs) == 0 {
		t.Fatal("no `go build -o \"$DIST_DIR/...\"` lines found in build.sh — this test " +
			"would pass vacuously; re-point it at however the build names its output")
	}

	var checked int
	for _, m := range outputs {
		name := m[1]
		// Only the ones a Windows machine could be installing from: the native
		// build (`current` → helix) and the windows cross-build.
		if name != "helix" && !strings.Contains(name, "windows") {
			continue
		}
		checked++
		if !strings.Contains(ps1, `".\dist\`+name+`"`) {
			t.Errorf("build.sh produces dist/%s but install.ps1 does not look for it; "+
				"a build that succeeds is followed by a copy that cannot find its source", name)
		}
	}
	if checked == 0 {
		t.Fatal("build.sh produced no Windows-relevant output names — re-point this test")
	}
}

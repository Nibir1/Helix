// cmd/helix/staleness.go
// Purpose: tell the user when the Helix they are TALKING TO is not the Helix
// that is on disk.
//
// This exists because it cost a real session. A fix was built, installed and
// verified, and the owner's shell went on exhibiting the old behaviour — they
// reported the bug again, with a screenshot, and the screenshot was the proof:
// its banner was missing a row that the binary on disk contained. Two different
// ways to be stale were at work across that session, and neither is visible
// from inside a running shell:
//
//	REPLACED   The file this process was started from has been overwritten
//	           since. A running process keeps its own image — replacing the
//	           file changes nothing until it restarts. /reboot fixes it.
//
//	UNINSTALLED  `make current` builds to dist/ and installs nothing. A
//	           maintainer who rebuilds and forgets `make install` is running a
//	           binary older than the one they just compiled, and every test
//	           they do by hand is against the old one.
//
// Both are one stat() to detect and neither has any other surface. The check is
// deliberately silent when everything agrees: a row that says "not stale" on
// every healthy machine is a row people learn to skip.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"helix/internal/update"
)

// processStarted is when this process began, near enough.
//
// Set in an init so it is the earliest thing Helix knows about itself. Compared
// against the executable's mtime: a file whose mtime is LATER than this was
// written after the process read it, which is exactly the replaced case. The
// alternative — asking the OS for a real process start time — is three
// platform-specific implementations for a comparison that is already accurate
// to well under the granularity that matters here.
var processStarted = time.Now()

// stalenessReport is what the check found, or the zero value when all is well.
type stalenessReport struct {
	// Replaced is set when the running binary's file has changed on disk.
	Replaced bool

	// NewerBuild is a path to a locally built binary newer than the running
	// one, or empty. The `make current` without `make install` case.
	NewerBuild string

	// Path is the running executable.
	Path string

	// Age is how much newer the thing on disk is.
	Age time.Duration
}

// stale reports whether anything is out of date.
func (s stalenessReport) stale() bool { return s.Replaced || s.NewerBuild != "" }

// checkStaleBinary compares the running process against what is on disk.
//
// Every failure is silent and returns "not stale". A diagnostic that cannot
// read something must not turn that into an accusation — §13 records the
// readiness check that blamed a missing ffmpeg for a startup-ordering bug, and
// this is the same shape of mistake one stat() away.
func checkStaleBinary() stalenessReport {
	var out stalenessReport

	exe, err := os.Executable()
	if err != nil {
		return out
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	out.Path = exe

	info, err := os.Stat(exe)
	if err != nil {
		// The binary is gone — uninstalled while running, most likely. Not a
		// staleness claim, and not something to guess about.
		return out
	}
	if info.ModTime().After(processStarted) {
		out.Replaced = true
		out.Age = info.ModTime().Sub(processStarted)
	}

	// A locally built binary newer than the running one. Only looked for
	// relative to the working directory, because that is where a maintainer's
	// checkout is; a user of a prebuilt binary has no dist/ and gets nothing.
	if build := newerLocalBuild(info.ModTime(), exe); build != "" {
		out.NewerBuild = build
	}
	return out
}

// newerLocalBuild returns a dist/helix under the working directory that is
// newer than the running binary, or "".
func newerLocalBuild(running time.Time, exe string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	// Walk up looking for a checkout, so `/doctor` works from a subdirectory
	// the way every other repo-aware tool does. Bounded, because an unbounded
	// walk from / on a deep tree is a syscall storm for a diagnostic nobody
	// asked to be thorough.
	// The candidate names come from internal/update, which already knows where
	// a local build lands (dist/helix, ./helix, bin/helix, plus the .exe
	// spelling on Windows). A second list here would be a second thing to keep
	// in step with scripts/build.sh.
	for range 6 {
		for _, rel := range update.LocalCandidatePaths() {
			candidate := filepath.Join(cwd, rel)
			info, err := os.Stat(candidate)
			if err != nil {
				continue
			}
			if same, _ := sameFile(candidate, exe); same {
				return "" // running the build itself; nothing to install
			}
			if info.ModTime().After(running) {
				return candidate
			}
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			break
		}
		cwd = parent
	}
	return ""
}

// sameFile reports whether two paths are the same file on disk.
func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// stalenessLines describe the problem and the fix, or nil when there is none.
func stalenessLines(r stalenessReport) []string {
	if !r.stale() {
		return nil
	}
	var out []string
	if r.Replaced {
		out = append(out,
			fmt.Sprintf("This process started before %s was last written (%s ago).",
				r.Path, compactAge(r.Age)),
			"A running Helix keeps its own copy, so the change you installed is not",
			"the one answering you. /reboot restarts into it.")
	}
	if r.NewerBuild != "" {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out,
			fmt.Sprintf("%s is newer than the binary you are running.", r.NewerBuild),
			"`make current` builds; it does not install. `make install` copies it",
			"into place, or run the build directly.")
	}
	return out
}

// compactAge renders a duration the way a person would say it.
func compactAge(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.0f hours", d.Hours())
	case d >= time.Minute:
		return fmt.Sprintf("%.0f minutes", d.Minutes())
	default:
		return "moments"
	}
}

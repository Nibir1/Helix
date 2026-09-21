// cmd/helix/staleness_test.go
// Purpose: the two ways a running Helix can be out of date, each reproduced
// against real files in a temp directory.
//
// Both cost a real session. A fix was built, installed and verified while the
// owner's shell went on showing the old behaviour, and the bug was reported
// twice before a screenshot proved the process — not the binary — was stale.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"helix/internal/update"
)

// distBinary is the path a local build lands at under dir.
//
// Asked of internal/update rather than spelled "dist/helix" here, because
// that is the list newerLocalBuild itself walks and on Windows it is
// "dist\helix.exe". Hardcoding the Unix name made the newer-build test fail
// on windows-latest and — worse — made the two tests either side of it pass
// VACUOUSLY there: they assert that nothing is reported, and nothing was,
// because the file they wrote was never a candidate (§9 rule 8).
func distBinary(dir string) string {
	return filepath.Join(dir, update.LocalCandidatePaths()[0])
}

// touch writes a file with a specific modification time.
func touch(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// A healthy machine must print NOTHING. A row that appears on every install is
// a row people stop reading, and this one has to be noticed the one time it
// matters.
func TestAFreshInstallReportsNoStaleness(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	saved := processStarted
	t.Cleanup(func() { processStarted = saved })

	// Executable written BEFORE the process started: the normal case.
	processStarted = time.Now()
	report := checkStaleBinary()
	if report.stale() {
		t.Errorf("a normal run reports stale: %+v", report)
	}
	if lines := stalenessLines(report); lines != nil {
		t.Errorf("a normal run prints %d lines", len(lines))
	}
}

// THE CASE FROM THE SESSION: the file was replaced while the process ran.
func TestAReplacedBinaryIsReported(t *testing.T) {
	saved := processStarted
	t.Cleanup(func() { processStarted = saved })
	// The test binary itself is the executable; pretend the process started
	// long before it was written.
	processStarted = time.Now().Add(-48 * time.Hour)

	report := checkStaleBinary()
	if !report.Replaced {
		t.Fatal("a binary written after the process started is not reported; this is the " +
			"case where an installed fix is not the one answering you")
	}
	lines := strings.Join(stalenessLines(report), " ")
	if !strings.Contains(lines, "/reboot") {
		t.Errorf("the advice does not name the fix: %q", lines)
	}
	if !strings.Contains(lines, "keeps its own copy") {
		t.Errorf("the advice does not explain WHY replacing the file changed nothing: %q", lines)
	}
}

// THE OTHER CASE: `make current` builds, and installs nothing.
func TestALocalBuildNewerThanTheRunningBinaryIsReported(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	saved := processStarted
	t.Cleanup(func() { processStarted = saved })
	processStarted = time.Now()

	// A dist/ build from the future relative to the running binary.
	touch(t, distBinary(dir), time.Now().Add(24*time.Hour))

	report := checkStaleBinary()
	if report.NewerBuild == "" {
		t.Fatal("a newer dist/helix is not reported; a maintainer who forgets `make install` " +
			"tests every fix against the old binary")
	}
	lines := strings.Join(stalenessLines(report), " ")
	if !strings.Contains(lines, "make install") {
		t.Errorf("the advice does not name the fix: %q", lines)
	}
	if !strings.Contains(lines, "does not install") {
		t.Errorf("the advice does not say what `make current` actually does: %q", lines)
	}
}

// An OLDER local build is not news. Someone who built yesterday and installed
// today must not be told their install is behind.
func TestAnOlderLocalBuildIsNotReported(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	saved := processStarted
	t.Cleanup(func() { processStarted = saved })
	processStarted = time.Now()

	touch(t, distBinary(dir), time.Now().Add(-48*time.Hour))
	if report := checkStaleBinary(); report.NewerBuild != "" {
		t.Errorf("an older build is reported as newer: %q", report.NewerBuild)
	}
}

// Running the build ITSELF is the maintainer's normal loop — `./dist/helix`
// after `make current`. Telling them to install it would be noise on every run.
func TestRunningTheLocalBuildItselfIsNotStale(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path")
	}
	dir := t.TempDir()
	chdir(t, dir)

	// Hard-link the running test binary in as dist/helix so os.SameFile sees
	// one file under two names, which is what `./dist/helix` really is.
	dst := distBinary(dir)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(exe, dst); err != nil {
		t.Skipf("cannot hard-link the test binary here: %v", err)
	}
	if got := newerLocalBuild(time.Now().Add(-time.Hour), exe); got != "" {
		t.Errorf("running the build itself reports %q as something to install", got)
	}
}

// Every failure must be silent. A diagnostic that cannot read something must
// not turn that into an accusation — §13 records the readiness check that
// blamed a missing ffmpeg for a startup-ordering bug.
func TestAnUnreadableTreeIsNotAnAccusation(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	saved := processStarted
	t.Cleanup(func() { processStarted = saved })
	processStarted = time.Now()

	// No dist/, nothing to find, and the walk must simply end.
	if report := checkStaleBinary(); report.NewerBuild != "" {
		t.Errorf("an empty tree produced a claim: %+v", report)
	}
}

func TestCompactAgeReadsLikeAPersonSayingIt(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Second: "moments",
		5 * time.Minute:  "5 minutes",
		3 * time.Hour:    "3 hours",
		50 * time.Hour:   "2 days",
	} {
		if got := compactAge(d); got != want {
			t.Errorf("compactAge(%v) = %q, want %q", d, got, want)
		}
	}
}

// chdir moves to dir for the test and restores afterwards.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// The detail names a PATH, and /doctor's panel is ~92 columns. Printed with
// PanelLine it ran 190 columns past the border while the CONFIG row two lines
// below wrapped correctly — found by rendering it, invisible in source
// (§9 rule 12). StepDetail is the existing wrap-with-indent helper.
func TestTheStaleRowWrapsInsideThePanel(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "handlers.go"), "func handleDoctor("))
	idx := strings.Index(body, "stalenessLines(report)")
	if idx < 0 {
		t.Fatal("/doctor no longer prints the staleness detail")
	}
	after := body[idx:]
	if strings.Contains(after[:200], "PanelLine(") {
		t.Error("the staleness detail is printed with PanelLine, which does not wrap; a long " +
			"binary path overruns the panel border")
	}
	if !strings.Contains(after[:200], "StepDetail(") {
		t.Error("the staleness detail does not use the wrap-with-indent helper")
	}
}

// It must come FIRST. A stale binary invalidates every row beneath it, and the
// user is most likely running /doctor because something they just fixed is
// still broken.
func TestTheStaleRowLeadsTheDoctorPanel(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "handlers.go"), "func handleDoctor("))
	stale := strings.Index(body, "checkStaleBinary()")
	// The ROW, not the width calculation. `"CONFIG"` also appears in the
	// KVWidth call at the top of the function, which made the first version of
	// this test fail against correct code — the same match-the-wrong-role
	// mistake as the "$0.05/min while open" one.
	config := strings.Index(body, `shell.KV("CONFIG"`)
	if stale < 0 {
		t.Fatal("/doctor does not check for a stale binary")
	}
	if config >= 0 && stale > config {
		t.Error("the staleness check runs after CONFIG; an 'ok' above a stale-binary warning " +
			"is a report about a Helix that is not answering you")
	}
}

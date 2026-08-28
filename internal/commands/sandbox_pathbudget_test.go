// internal/commands/sandbox_pathbudget_test.go
//
// Purpose: keep ValidateCommand's cost bounded by the number of paths it must
// actually decide about, not by the length of the string it was handed.
//
// These are regression tests for a stall a fuzzer found, not for a wrong
// answer. ValidateSafePath resolves symlinks, which is a chain of lstat calls
// per path; the old ValidateCommand ran it for every absolute-looking word in
// EVERY command and only afterwards asked whether the command was a write
// operation at all. Read-only commands therefore paid the full filesystem bill
// and discarded the result — 2.2s for a 150KB input on a warm SSD, and far
// worse on the slow storage this project targets at the edge.
//
// Wall-clock in a test is normally a flake generator, so these bounds are
// deliberately loose: they are three orders of magnitude above what the fixed
// code needs and still far below what the old code took. They fail on a
// regression in ALGORITHM, not on a slow machine.
package commands

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func newBudgetSandbox(t *testing.T) *DirectorySandbox {
	t.Helper()
	tmp := t.TempDir()
	// Run FROM the sandbox, as the shell actually does. It matters: relative
	// words — including the command name itself, which extractFileArguments
	// returns like any other argument — are resolved against the working
	// directory, so a test run from the package directory would see "rm"
	// itself as a path outside the sandbox.
	t.Chdir(tmp)
	return &DirectorySandbox{allowedDir: tmp, mode: SandboxCurrentDir, originalDir: tmp}
}

// A read-only command must not touch the filesystem once per word. Before the
// fix this took seconds; it now takes microseconds.
func TestReadOnlyCommandDoesNotResolveEveryWord(t *testing.T) {
	ds := newBudgetSandbox(t)

	// DISTINCT paths, so memoisation cannot mask a regression: only skipping
	// the work entirely is fast here. The old code resolved every one of these
	// and discarded the answer.
	var b strings.Builder
	b.WriteString("grep -r")
	for i := 0; i < 200_000; i++ {
		fmt.Fprintf(&b, " /nonexistent/p%d", i)
	}
	input := b.String()

	start := time.Now()
	allowed, msg := ds.ValidateCommand(input)
	elapsed := time.Since(start)

	if !allowed {
		t.Fatalf("read-only command refused: %q", msg)
	}
	// Three orders of magnitude above what the fixed code needs (~1ms) and far
	// below what the old code took (>10s for this input). Only an algorithmic
	// regression can cross it.
	if elapsed > 3*time.Second {
		t.Fatalf("read-only validation took %v for %d bytes — the per-word "+
			"filesystem work is back", elapsed, len(input))
	}
}

// The budget must fail CLOSED. A command naming more distinct paths than the
// sandbox will resolve is refused, never quietly permitted.
func TestPathBudgetRefusesRatherThanSkips(t *testing.T) {
	ds := newBudgetSandbox(t)

	var b strings.Builder
	b.WriteString("rm ")
	for i := 0; i < maxDistinctPathChecks*4; i++ {
		// Inside the sandbox, so each check SUCCEEDS and nothing
		// short-circuits — the only way to reach the cap.
		fmt.Fprintf(&b, "%s/p%d ", strings.ToLower(ds.allowedDir), i)
	}

	start := time.Now()
	allowed, msg := ds.ValidateCommand(b.String())
	elapsed := time.Since(start)

	if allowed {
		t.Fatal("command exceeding the path budget was PERMITTED — the budget " +
			"must fail closed, since paths past the cap were never checked")
	}
	if msg != pathBudgetRefusal {
		t.Fatalf("refused for the wrong reason: %q", msg)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("budgeted validation still took %v — the cap is not bounding "+
			"the work", elapsed)
	}
}

// Repeating one path must cost one resolution, not one per occurrence.
func TestRepeatedPathIsResolvedOnce(t *testing.T) {
	ds := newBudgetSandbox(t)
	one := "rm " + strings.ToLower(ds.allowedDir) + "/a"
	many := "rm " + strings.Repeat(strings.ToLower(ds.allowedDir)+"/a ", maxDistinctPathChecks*4)

	allowedOne, _ := ds.ValidateCommand(one)
	allowedMany, msg := ds.ValidateCommand(many)

	// One distinct path, so the budget must not trigger however often it repeats.
	if allowedOne != allowedMany {
		t.Fatalf("repetition changed the verdict: one=%v many=%v (%q)",
			allowedOne, allowedMany, msg)
	}
	if msg == pathBudgetRefusal {
		t.Fatal("a single repeated path exhausted the budget — results are not memoised")
	}
}

// The hoisted check must not have changed any verdict.
func TestHoistingPreservedVerdicts(t *testing.T) {
	ds := newBudgetSandbox(t)
	in := ds.allowedDir

	cases := []struct {
		command string
		allowed bool
	}{
		{"ls -la", true},
		{"cat /etc/passwd", true},        // read-only, outside: still allowed
		{"grep -r /usr /var /opt", true}, // read-only, many absolute paths
		{"rm /etc/passwd", false},        // write, outside
		{"echo hi > /etc/passwd", false}, // redirect, outside
		{"cd ../..", false},              // escape
		{"mv file.txt ../", false},       // escape
		{"rm " + in + "/a", true},        // write, inside
		{"chmod 777 " + in + "/s.sh", true},
	}
	for _, c := range cases {
		allowed, msg := ds.ValidateCommand(c.command)
		if allowed != c.allowed {
			t.Errorf("ValidateCommand(%q) = %v (%q), want %v", c.command, allowed, msg, c.allowed)
		}
	}
}

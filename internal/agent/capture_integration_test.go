//go:build !windows

// internal/agent/capture_integration_test.go
// Purpose: BlackBox P8.6 — output capture is wired end-to-end through the real
// execution path (executePlanSteps → sandbox → shell), and is gated on agentic
// mode so a normal turn keeps the child's inherited file descriptors.
package agent

import (
	"os"
	"strings"
	"testing"

	"helix/internal/ai"
)

// runStepsQuietly executes a plan through the real pipeline with stdout/stderr
// pointed at /dev/null, so the test does not spray command output into the
// test log while still exercising the genuine tee path.
func runStepsQuietly(t *testing.T, ag *Agent, plan *ai.Plan) []StepObservation {
	t.Helper()

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("devnull: %v", err)
	}
	realOut, realErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = devNull, devNull
	defer func() {
		os.Stdout, os.Stderr = realOut, realErr
		_ = devNull.Close()
	}()

	return ag.executePlanSteps(plan, map[string]bool{})
}

func TestAgenticModeCapturesStepOutput(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	ag, _ := newTestAgent(t)
	ag.Agentic = true

	obs := runStepsQuietly(t, ag, &ai.Plan{Steps: []ai.PlanStep{
		{Tool: "shell", Command: "echo helix-observed-output"},
	}})

	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
	// The point of P8.6: the trace carries WHAT the command printed, not just
	// that it exited 0.
	if !strings.Contains(obs[0].Output, "helix-observed-output") {
		t.Fatalf("agentic mode must capture step output, got %q", obs[0].Output)
	}
	if !obs[0].OK {
		t.Fatalf("step should have succeeded: %s", obs[0].Err)
	}
}

// The default path must be untouched: with the harness off nothing consumes
// the tail, and capturing would cost the child its TTY for no benefit.
func TestNonAgenticModeCapturesNothing(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	ag, _ := newTestAgent(t)
	ag.Agentic = false

	obs := runStepsQuietly(t, ag, &ai.Plan{Steps: []ai.PlanStep{
		{Tool: "shell", Command: "echo should-not-be-captured"},
	}})

	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
	if obs[0].Output != "" {
		t.Fatalf("non-agentic turns must not capture output, got %q", obs[0].Output)
	}
}

// A failing command's stderr is the diagnosis the next plan needs, and the
// failure itself must be visible to the harness.
//
// This pins the split that leniency forces: execution deliberately does NOT
// error on a non-zero exit (so the user is not nagged about `grep` finding
// nothing), so `OK` stays true — and `ExitCode` is what tells the planner the
// command actually failed. Without the exit code the harness would see an
// all-OK trace and stop instead of self-correcting.
func TestFailedStepOutputReachesObservation(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	ag, _ := newTestAgent(t)
	ag.Agentic = true

	obs := runStepsQuietly(t, ag, &ai.Plan{Steps: []ai.PlanStep{
		{Tool: "shell", Command: "echo diagnostic-detail >&2; exit 3"},
	}})

	if len(obs) != 1 {
		t.Fatalf("expected 1 observation, got %d", len(obs))
	}
	if obs[0].ExitCode != 3 {
		t.Fatalf("the true exit code must survive leniency, got %d", obs[0].ExitCode)
	}
	if allStepsOK(obs) {
		t.Fatal("a non-zero exit must make the harness replan, not stop")
	}
	if !strings.Contains(obs[0].Output, "diagnostic-detail") {
		t.Fatalf("stderr from a failing step must reach the planner, got %q", obs[0].Output)
	}

	// And it must render into the data-only block the harness feeds back.
	block := observationBlock(obs)
	if !strings.Contains(block, "diagnostic-detail") {
		t.Fatal("captured output must appear in the observation block")
	}
	if !strings.Contains(block, "exit_code=3") {
		t.Fatalf("the exit code must be reported to the planner:\n%s", block)
	}
}

// The counterpart: a clean exit must still let the harness stop, or every
// successful turn would burn its full step budget.
func TestSuccessfulStepKeepsHarnessComplete(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	ag, _ := newTestAgent(t)
	ag.Agentic = true

	obs := runStepsQuietly(t, ag, &ai.Plan{Steps: []ai.PlanStep{
		{Tool: "shell", Command: "echo fine"},
	}})

	if obs[0].ExitCode != 0 {
		t.Fatalf("a clean command must report exit 0, got %d", obs[0].ExitCode)
	}
	if !allStepsOK(obs) {
		t.Fatal("a fully successful trace must end the harness loop")
	}
}

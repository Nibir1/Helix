//go:build !windows

// internal/agent/planner_trust_test.go
// Purpose: a medium-risk step the PLANNER proposed asks before it runs.
//
// From v1.0.0 every planner step was marked Trusted unless the firewall
// escalated it, and Trusted skips the medium-risk confirmation, so writes and
// medium-risk shell commands ran without asking under the default `ask`
// posture. Nothing caught it: the tests exercised the gates through
// handle*Step directly, never through executePlanSteps, where the trust was
// assigned. It was found on 2026-10-01 when a planner `file write` went straight
// through the daemon's always-decline prompter.
//
// These tests drive the real dispatcher, so the property is pinned where it is
// decided.
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
)

// gateAgent returns an agent whose sandbox is rooted in a fresh directory and
// whose prompter answers `answer`, plus that directory and the prompter.
func gateAgent(t *testing.T, answer bool) (*Agent, string, *recordingPrompter) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	ag, _ := newTestAgent(t)
	p := &recordingPrompter{answer: answer}
	restore := commands.ActivePrompter()
	commands.SetPrompter(p)
	t.Cleanup(func() { commands.SetPrompter(restore) })
	return ag, dir, p
}

func planWrite(name string) *ai.Plan {
	return &ai.Plan{Steps: []ai.PlanStep{{
		Tool: "file", Action: "write",
		Args: map[string]string{"path": name, "content": "hello\n"},
	}}}
}

func planShellRedirect(name string) *ai.Plan {
	return &ai.Plan{Steps: []ai.PlanStep{{Tool: "shell", Command: "echo hello >" + name}}}
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestPlannerMediumStepsAskUnderAsk(t *testing.T) {
	for name, plan := range map[string]*ai.Plan{
		"file write":     planWrite("planned.txt"),
		"shell redirect": planShellRedirect("planned.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			ag, dir, p := gateAgent(t, false) // the user says no
			runStepsQuietly(t, ag, plan)

			if len(p.asked) == 0 {
				t.Fatal("a medium-risk planner step ran without asking under the default ask posture")
			}
			if exists(dir, "planned.txt") {
				t.Fatal("the step ran even though the confirmation was declined")
			}
		})
	}
}

func TestPlannerMediumStepRunsWhenApproved(t *testing.T) {
	ag, dir, p := gateAgent(t, true)
	runStepsQuietly(t, ag, planWrite("approved.txt"))
	if len(p.asked) == 0 || !exists(dir, "approved.txt") {
		t.Fatalf("asked=%v exists=%v: an approved write must ask once and then run",
			p.asked, exists(dir, "approved.txt"))
	}
}

// /permissions auto is the chosen, persisted, announced way to skip the
// question, and the fix must not take it away.
func TestPlannerMediumStepRunsUnderAuto(t *testing.T) {
	ag, dir, p := gateAgent(t, false)
	ag.SetPermission(PermissionAuto)
	runStepsQuietly(t, ag, planWrite("auto.txt"))
	if len(p.asked) != 0 {
		t.Fatalf("auto posture asked anyway: %v", p.asked)
	}
	if !exists(dir, "auto.txt") {
		t.Fatal("auto posture did not run the write")
	}
}

// Trusted remains what it was designed for: plans Helix builds itself.
func TestDeterministicTrustedStepStillAutoConfirms(t *testing.T) {
	ag, dir, p := gateAgent(t, false)
	if err := ag.handleShellStep(ai.PlanStep{Tool: "shell", Command: "echo hello >fast.txt", Trusted: true}); err != nil {
		t.Fatal(err)
	}
	if len(p.asked) != 0 || !exists(dir, "fast.txt") {
		t.Fatalf("asked=%v exists=%v: a fast-path trusted step should run without asking",
			p.asked, exists(dir, "fast.txt"))
	}
}

// Low risk still runs without a question; the fix must not start nagging.
func TestPlannerLowRiskStepDoesNotAsk(t *testing.T) {
	ag, dir, p := gateAgent(t, false)
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	obs := runStepsQuietly(t, ag, &ai.Plan{Steps: []ai.PlanStep{
		{Tool: "file", Action: "read", Args: map[string]string{"path": "readme.txt"}},
	}})
	if len(p.asked) != 0 || len(obs) != 1 || !obs[0].OK {
		t.Fatalf("asked=%v obs=%+v: a read should run without a question", p.asked, obs)
	}
}

// executePlanSteps must not mark planner steps Trusted at all.
func TestExecutePlanStepsDoesNotTrustThePlanner(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"step.Trusted = !escalated", "step.Trusted = true"} {
		if strings.Contains(string(src), bad) {
			t.Fatalf("agent.go assigns %q: planner steps would skip the medium-risk confirmation", bad)
		}
	}
}

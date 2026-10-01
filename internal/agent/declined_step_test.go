//go:build !windows

// internal/agent/declined_step_test.go
// Purpose: a step the user said no to is reported as declined, never as OK.
//
// The handlers print "skipped" and return nil when a confirmation is refused,
// and the dispatcher used to read nil as success: the observation said OK, the
// agentic planner was told the change had been made, the remaining steps ran on
// top of a change that never happened, and Metabolism recorded the turn as a
// success. These tests run the real dispatcher under the `cautious` posture,
// which asks even for low-risk steps, so a refusal is easy to provoke.
package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/metabolism"
)

func cautiousAgent(t *testing.T, p commands.Prompter) *Agent {
	t.Helper()
	t.Chdir(t.TempDir())
	ag, _ := newTestAgent(t)
	ag.SetPermission(PermissionCautious)
	restore := commands.ActivePrompter()
	commands.SetPrompter(p)
	t.Cleanup(func() { commands.SetPrompter(restore) })
	return ag
}

var twoSteps = &ai.Plan{Steps: []ai.PlanStep{
	{Tool: "shell", Command: "echo first"},
	{Tool: "shell", Command: "echo second"},
}}

func TestDeclinedStepIsNotOKAndStopsThePlan(t *testing.T) {
	p := &recordingPrompter{answer: false}
	ag := cautiousAgent(t, p)
	obs := runStepsQuietly(t, ag, twoSteps)

	if len(p.asked) != 1 {
		t.Fatalf("asked %d times, want once (the plan must stop at the first refusal)", len(p.asked))
	}
	if len(obs) != 1 {
		t.Fatalf("%d steps reported; the second must not run after the first was declined", len(obs))
	}
	o := obs[0]
	if o.OK || !o.Declined || !strings.HasPrefix(o.Err, "declined:") {
		t.Fatalf("a declined step was reported as %+v", o)
	}
}

func TestApprovedStepsAreOK(t *testing.T) {
	ag := cautiousAgent(t, &recordingPrompter{answer: true})
	obs := runStepsQuietly(t, ag, twoSteps)
	if len(obs) != 2 || !obs[0].OK || !obs[1].OK || obs[0].Declined || obs[1].Declined {
		t.Fatalf("approved steps: %+v", obs)
	}
}

// Nobody said no to the daemon: its prompter refuses by policy. The step is
// still not OK, but it must not be reported as the user declining.
func TestUnattendedRefusalIsRefusedNotDeclined(t *testing.T) {
	ag := cautiousAgent(t, unattendedPrompter{})
	obs := runStepsQuietly(t, ag, twoSteps)
	if len(obs) != 1 || obs[0].OK || !obs[0].Declined || !strings.HasPrefix(obs[0].Err, "refused:") {
		t.Fatalf("unattended refusal reported as %+v", obs)
	}
}

// A refusal ends the run: the loop must not replan around the user's "no".
func TestRefusalEndsTheRunAsOpenWork(t *testing.T) {
	declined := []StepObservation{{Tool: "shell", OK: false, Declined: true, Err: "declined: ..."}}
	if !followUpDone(declined, true) {
		t.Fatal("the harness would replan after a refusal")
	}
	ag, _ := newTestAgent(t)
	ag.reportRunEnd(declined, 0, 4, false)
	if ag.turnEnd != metabolism.EndOpenWork {
		t.Fatalf("a declined run ended as %q, want open-work (not a failure)", ag.turnEnd)
	}
}

// End to end for the record: the step is not OK, the turn is open work, and
// the person's "no" is a declined outcome.
func TestDeclinedTurnIsRecordedHonestly(t *testing.T) {
	ag := cautiousAgent(t, &recordingPrompter{answer: false})
	rec, err := metabolism.Open(metabolism.Options{
		Dir: filepath.Join(t.TempDir(), "m"), Enabled: true, Declines: commands.DeclinedConfirmations,
	})
	if err != nil {
		t.Fatal(err)
	}
	ag.Metabolism = rec

	ag.beginEpisode()
	ag.turnPlanned = true
	ag.recordSteps(runStepsQuietly(t, ag, twoSteps))
	ag.finishEpisode(input.InputEvent{Text: "say first then second"})

	recs := records(t, rec)
	eps := episodes(recs)
	if len(eps) != 1 {
		t.Fatalf("episodes: %+v", eps)
	}
	ep := eps[0]
	if ep.End != metabolism.EndOpenWork || len(ep.Steps) != 1 || ep.Steps[0].OK ||
		!strings.HasPrefix(ep.Steps[0].Err, "declined:") {
		t.Fatalf("recorded as %+v", ep)
	}
	var declinedOutcome bool
	for _, r := range recs {
		if r.Outcome != nil && r.Outcome.Kind == metabolism.OutcomeDeclined {
			declinedOutcome = true
		}
	}
	if !declinedOutcome {
		t.Fatal("no declined outcome recorded for the user's no")
	}
}

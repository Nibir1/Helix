package agent

import (
	"strings"

	"helix/internal/ai"
)

// Multi-round replay (Metabolism D-021).
//
// A first plan is often not where a turn goes wrong. A real run that ran out
// of budget looked fine in its first plan (one glob, one grep) and went wrong
// in its follow-up rounds, rereading the same file and repeating the same
// grep. Lessons about that cannot be tested on a first plan, so a replay can
// play the follow-up rounds too, the way a live non-agentic turn does:
//
//  1. Plan.
//  2. If every step of the plan is a read-only file step, run those steps.
//     Anything else ends the replay right there, unexecuted.
//  3. If the reads found something to answer from (needsAnswer), plan again
//     with their results in the same fenced execution report a live turn
//     sends, for at most the live follow-up budget (retrievalBudget: 3 for a
//     file lookup).
//
// Read-only means the file tool's list, glob, grep and read, nothing else. They
// change nothing on disk; they go through the same sandbox resolver as a live
// turn; and their output reaches the planner only inside the data-only report.
// Shell commands, git, package managers, writes, edits, web requests and the
// task list are never executed by a replay, whatever the round.

// Replay ends.
const (
	// ReplayEndPlanned: one round was asked for (replay v1), or the plan
	// had nothing for a replay to run.
	ReplayEndPlanned = "planned"
	// ReplayEndAnswered: the rounds stopped the way a live turn stops,
	// with nothing left to answer from.
	ReplayEndAnswered = "answered"
	// ReplayEndBudget: the follow-up budget ran out while the planner was
	// still reading.
	ReplayEndBudget = "budget-exhausted"
	// ReplayEndUnexecuted: a plan reached a step a replay never runs (a
	// shell command, a write). A live turn would have acted there.
	ReplayEndUnexecuted = "unexecuted-step"
)

// ReplayStep is one planned step, and what happened to it if it ran.
type ReplayStep struct {
	Step  ai.PlanStep
	Round int // 1-based
	Ran   bool
	OK    bool
	Err   string
}

// ReplayResult is a replay of one request over one or more rounds.
type ReplayResult struct {
	Steps  []ReplayStep
	Reply  string
	Rounds int
	End    string
}

// readOnlyFileStep reports whether a replay may run step.
func readOnlyFileStep(step ai.PlanStep) bool {
	if step.Tool != "file" {
		return false
	}
	switch strings.TrimSpace(step.Action) {
	case "list", "glob", "grep", "read":
		return true
	}
	return false
}

// ReplayRounds replays request with lessons for up to maxRounds rounds.
// maxRounds <= 1 is a first-plan replay that executes nothing (PlanReplay).
func (a *Agent) ReplayRounds(request string, lessons []LearnedLesson, maxRounds int) (*ReplayResult, error) {
	block := learnedLessonsBlock(lessons)
	res := &ReplayResult{End: ReplayEndPlanned}
	var obs []StepObservation
	budget := 0 // follow-up rounds allowed, fixed after round 1 as a live turn does
	for round := 1; ; round++ {
		turn := turnContext{}
		if round > 1 {
			turn = turnContext{Report: observationBlock(obs), Directive: observationDirective(obs)}
		}
		plan, err := a.planOnly(request, block, "HELIX :: REPLAYING", turn)
		if err != nil {
			if round == 1 {
				return nil, err
			}
			// A follow-up planner failure ends the replay where it is, as a
			// live turn stops when a follow-up plan fails.
			res.End = ReplayEndAnswered
			return res, nil
		}
		res.Rounds = round
		first := len(res.Steps)
		for _, st := range plan.Steps {
			res.Steps = append(res.Steps, ReplayStep{Step: st, Round: round, OK: true})
			if st.Tool == "response" && strings.TrimSpace(st.Message) != "" {
				res.Reply = strings.TrimSpace(st.Message)
			}
		}
		if maxRounds <= 1 {
			return res, nil
		}

		// Run the round only if every step is something a replay may run;
		// otherwise the replay ends where a live turn would have acted.
		obs = obs[:0]
		for i, st := range plan.Steps {
			switch {
			case st.Tool == "response":
				obs = append(obs, StepObservation{Index: i, Tool: st.Tool, OK: true})
			case readOnlyFileStep(st):
			default:
				res.End = ReplayEndUnexecuted
				return res, nil
			}
		}
		for i, st := range plan.Steps {
			if !readOnlyFileStep(st) {
				continue
			}
			o := StepObservation{Index: i, Tool: st.Tool, Action: st.Action, OK: true, Subject: stepSubject(st)}
			out, err := a.runFileAction(strings.TrimSpace(st.Action), st.Args)
			rs := &res.Steps[first+i]
			rs.Ran = true
			if err != nil {
				o.OK, o.Err = false, err.Error()
				rs.OK, rs.Err = false, err.Error()
			} else {
				o.Output = out
			}
			o.NeedsAnswer = true // a read's result is for the planner to use
			obs = append(obs, o)
		}

		if round == 1 {
			// A live non-agentic turn follows up only when a retrieval found
			// something to answer from, and its budget depends on what kind.
			if !needsAnswer(obs) {
				for _, rs := range res.Steps[first:] {
					if rs.Ran {
						res.End = ReplayEndAnswered
					}
				}
				return res, nil
			}
			budget = retrievalBudget(obs)
		}
		if followUpDone(obs, false) {
			res.End = ReplayEndAnswered
			return res, nil
		}
		if round-1 >= budget || round >= maxRounds {
			res.End = ReplayEndBudget
			return res, nil
		}
	}
}

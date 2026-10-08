package agent

import (
	"fmt"
	"math/rand/v2"
	"os"

	"helix/internal/ai"
	"helix/internal/input"
	"helix/internal/metabolism"
)

// This file turns a Helix turn into a Metabolism episode. It only observes:
// every function here reads agent state and hands it to the recorder, and
// none of it can change what the turn does. Recording is best-effort and
// nil-safe throughout, so a recorder that is off, missing or failing leaves
// the turn exactly as it was.

// MeterUsage adapts the model-usage meter to the recorder's counters. It lives
// here, not in internal/metabolism, because the meter's package reaches the
// network and the recorder's package must not (TestNoNetworkImports there).
func MeterUsage() metabolism.UsageTotals {
	rep := ai.Usage()
	t := metabolism.UsageTotals{Calls: rep.Calls, ByKind: map[string]int{}}
	for _, row := range rep.Rows {
		t.PromptChars += row.PromptChars
		t.ResponseChars += row.ResponseChars
		t.ByKind[string(row.Kind)] += row.Calls
		t.Tokens.Prompt += row.Reported.Prompt
		t.Tokens.CacheHit += row.Reported.CacheHit
		t.Tokens.Completion += row.Reported.Completion
		t.Tokens.Reasoning += row.Reported.Reasoning
		t.Tokens.Calls += row.ReportedCalls
	}
	return t
}

// beginEpisode resets the per-turn recording state and starts an episode
// (nil when recording is off).
func (a *Agent) beginEpisode() {
	a.turnPlanned = false
	a.turnEnd = ""
	a.lastObs = nil
	a.planRound = 0
	cwd, _ := os.Getwd()
	a.episode = a.Metabolism.Begin(cwd)
	a.flipThinking(coinFlip)
}

// thinkingSwitchable asks whether the active provider can plan without
// reasoning; a variable so tests need no provider.
var thinkingSwitchable = ai.ThinkingSwitchable

// coinFlip is a fair coin; a variable so tests can fix the arm.
var coinFlip = func() bool { return rand.IntN(2) == 0 }

// flipThinking puts the turn in one arm of the planning-mode measurement:
// thinking off when the coin says so, and the arm on the episode either way.
// Nothing happens unless the flip is on, the turn is recorded and the
// provider can switch its reasoning off.
func (a *Agent) flipThinking(coin func() bool) {
	ai.SetPlannerThinkingOff(false)
	if !a.FlipThinking || a.episode == nil || !thinkingSwitchable() {
		return
	}
	if coin() {
		ai.SetPlannerThinkingOff(true)
		a.episode.SetThinking(metabolism.ThinkingOff)
		a.render.PrintDebug("planning mode: thinking off for this turn (coin flip)")
		return
	}
	a.episode.SetThinking(metabolism.ThinkingOn)
}

// finishEpisode writes the turn, if it was planner experience.
func (a *Agent) finishEpisode(ev input.InputEvent) {
	ai.SetPlannerThinkingOff(false)
	turn := a.episode
	a.episode = nil
	if turn == nil || a.turnWasControl || !a.turnPlanned {
		return
	}
	a.Metabolism.Finish(turn, ev.Text, a.turnProvenance(), a.derivedRunEnd())
}

// recordSteps adds one iteration's executed steps to the episode.
func (a *Agent) recordSteps(obs []StepObservation) {
	a.lastObs = obs
	for _, o := range obs {
		subject := o.Subject
		if subject == "" {
			subject = o.Command
		}
		// A lenient non-zero exit ran without an error at the user, but it
		// did not succeed, and the record must say so.
		ok, errText := o.OK, o.Err
		if o.ExitCode != 0 {
			ok = false
			if errText == "" {
				errText = fmt.Sprintf("exit status %d", o.ExitCode)
			}
		}
		a.episode.AddStep(metabolism.Step{
			Tool:    o.Tool,
			Action:  o.Action,
			Subject: subject,
			OK:      ok,
			Err:     errText,
			Round:   a.planRound,
		})
	}
}

// stepSubject names what a step acted on, for the record. Shell and git
// steps already carry it in Command; file and web steps keep it in Args.
func stepSubject(step ai.PlanStep) string {
	switch step.Tool {
	case "file":
		return fileSubject(step.Action, step.Args)
	case "web":
		if u := step.Args["url"]; u != "" {
			return u
		}
		return step.Args["query"]
	}
	return step.Command
}

// turnProvenance labels where the request came from. A transcript below the
// voice confidence gate is a guess at what was said, and is labelled so.
func (a *Agent) turnProvenance() string {
	switch {
	case a.turnUnreliable:
		return metabolism.ProvUserVoiceUnreliable
	case a.voiceActive():
		return metabolism.ProvUserVoice
	}
	return metabolism.ProvUserTyped
}

// runEndFor maps reportRunEnd's cases onto the engine's run ends. open is
// "work left undone": tasks still open, or, when the budget ran out, a
// retrieval the model never got to answer. reportRunEnd only counts tasks, so
// a lookup that searched until the budget was spent and then said nothing
// printed no warning and was recorded as done. The first real recorded run
// was exactly that: one glob four times, then an empty reply, labelled a
// success.
func runEndFor(failed, open, exhausted bool) string {
	switch {
	case failed:
		return metabolism.EndFailed
	case open && exhausted:
		return metabolism.EndBudgetExhausted
	case open:
		return metabolism.EndOpenWork
	}
	return metabolism.EndDone
}

// unanswered reports whether the last iteration retrieved something and did
// not answer from it in the same plan.
func unanswered(obs []StepObservation) bool {
	if !needsAnswer(obs) {
		return false
	}
	for _, o := range obs {
		if o.Tool == "response" && o.OK {
			return false
		}
	}
	return true
}

// derivedRunEnd is how the turn ended. The agentic loop says so itself
// (reportRunEnd). A single-shot turn is judged from its steps: a failed step
// is a failure, and a turn that produced neither a step nor a reply (the
// planner errored and nothing answered) is a failure too.
func (a *Agent) derivedRunEnd() string {
	if a.turnEnd != "" {
		return a.turnEnd
	}
	if anyDeclined(a.lastObs) {
		return metabolism.EndOpenWork
	}
	if len(a.lastObs) == 0 {
		if a.lastResponse == "" {
			return metabolism.EndFailed
		}
		return metabolism.EndDone
	}
	if !allStepsOK(a.lastObs) {
		return metabolism.EndFailed
	}
	return metabolism.EndDone
}

// modelCalls is how many model calls the session has made, for measuring
// what one planning or review cost.
func modelCalls() int { return ai.Usage().Calls }

// modelSpend is the session's model calls and characters sent and received.
type modelSpend struct {
	calls   int
	in, out int64
	tokens  metabolism.Tokens // cumulative reported tokens
	spent   *metabolism.Tokens
}

func spendNow() modelSpend {
	u := MeterUsage()
	return modelSpend{calls: u.Calls, in: u.PromptChars, out: u.ResponseChars, tokens: u.Tokens}
}

// since is what was spent between s and now.
func (s modelSpend) since() modelSpend {
	n := spendNow()
	return modelSpend{calls: n.calls - s.calls, in: n.in - s.in, out: n.out - s.out,
		spent: metabolism.TokensSince(n.tokens, s.tokens)}
}

// recordPlan adds one planner round to the episode (wire v3, Metabolism
// D-031): which prompt planned it, what it cost, how it ended, and the
// plan's own routing labels. It only observes.
func (a *Agent) recordPlan(prompt string, spent modelSpend, result string, plan *ai.Plan) {
	p := metabolism.Plan{Round: a.planRound, Prompt: prompt, Calls: spent.calls, Result: result,
		InputChars: int(spent.in), OutputChars: int(spent.out), Tokens: spent.spent}
	if plan != nil {
		p.Intent = string(plan.Intent)
		p.Steps = len(plan.Steps)
		answered := len(plan.Steps) > 0
		for i, s := range plan.Steps {
			if i == 0 {
				p.FirstTool = s.Tool
			}
			if s.Tool != "response" {
				answered = false
			}
		}
		p.Answered = answered
	}
	a.episode.AddPlan(p)
}

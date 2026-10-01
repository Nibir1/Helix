package agent

import (
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
	t := metabolism.UsageTotals{Calls: rep.Calls}
	for _, row := range rep.Rows {
		t.PromptChars += row.PromptChars
		t.ResponseChars += row.ResponseChars
	}
	return t
}

// beginEpisode resets the per-turn recording state and starts an episode
// (nil when recording is off).
func (a *Agent) beginEpisode() {
	a.turnPlanned = false
	a.turnEnd = ""
	a.lastObs = nil
	cwd, _ := os.Getwd()
	a.episode = a.Metabolism.Begin(cwd)
}

// finishEpisode writes the turn, if it was planner experience.
func (a *Agent) finishEpisode(ev input.InputEvent) {
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
		a.episode.AddStep(metabolism.Step{
			Tool:    o.Tool,
			Action:  o.Action,
			Subject: subject,
			OK:      o.OK,
			Err:     o.Err,
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

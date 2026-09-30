package agent

import (
	"os"

	"helix/internal/input"
	"helix/internal/metabolism"
)

// This file turns a Helix turn into a Metabolism episode. It only observes:
// every function here reads agent state and hands it to the recorder, and
// none of it can change what the turn does. Recording is best-effort and
// nil-safe throughout, so a recorder that is off, missing or failing leaves
// the turn exactly as it was.

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
		a.episode.AddStep(metabolism.Step{
			Tool:    o.Tool,
			Action:  o.Action,
			Subject: o.Command,
			OK:      o.OK,
			Err:     o.Err,
		})
	}
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

// runEndFor maps reportRunEnd's four cases onto the engine's run ends. It is
// the same decision reportRunEnd prints, so the record and the screen agree.
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

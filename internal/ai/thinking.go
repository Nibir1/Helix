package ai

import (
	"sync/atomic"

	"helix/internal/providers"
)

// The planner's thinking switch, for Metabolism's planning-mode measurement
// (D-031): does the planner need to reason before every plan, or do turns
// succeed as often without it, at a fraction of the tokens? A turn that loses
// a coin flip plans with thinking off; the recording says which it was, and
// the engine compares the two arms.
//
// It only ever asks the provider to skip its reasoning. Nothing about what a
// plan may do changes: every step still goes through validation, risk tiers
// and confirmation.

var plannerThinkingOff atomic.Bool

// SetPlannerThinkingOff switches reasoning off (true) or back on for the
// planner calls that follow. Helix runs one turn at a time; the agent sets it
// at the start of a turn and clears it at the end.
func SetPlannerThinkingOff(off bool) { plannerThinkingOff.Store(off) }

// PlannerThinkingOff reports whether planner calls ask for no reasoning.
func PlannerThinkingOff() bool { return plannerThinkingOff.Load() }

// ThinkingSwitchable reports whether the active provider honours the switch.
// The coin is only flipped where it does: a flip with no effect would record
// "off" turns that thought anyway.
func ThinkingSwitchable() bool {
	p, _, _ := resolveProvider()
	return p != nil && providers.CanSwitchThinking(p)
}

// internal/agent/permission.go
// Purpose: the harness's approval policy — how much the agent may do without
// asking, expressed as one mode instead of scattered toggles.
//
// The mode LAYERS ON TOP of the risk tiers; it never replaces them. High-risk
// commands stay blocked in every mode, typed confirmations stay typed, the
// Voice Risk Policy still caps voice-originated plans, and the sandbox still
// validates every command. What the mode changes is only the question Helix
// asks about the commands it was already willing to consider.
package agent

import "strings"

// PermissionMode is the session's approval posture.
type PermissionMode string

const (
	// PermissionPlan never executes. Steps are printed as a plan and dropped.
	// This is the read-only mode: safe to point at an unfamiliar request.
	PermissionPlan PermissionMode = "plan"

	// PermissionCautious confirms every command, including low risk.
	PermissionCautious PermissionMode = "cautious"

	// PermissionAsk is the default: low risk runs, medium risk asks, high
	// risk is blocked.
	PermissionAsk PermissionMode = "ask"

	// PermissionAuto answers the medium-risk prompt for you. High risk is
	// still blocked and typed confirmations are still typed — this removes
	// nagging, not guardrails.
	PermissionAuto PermissionMode = "auto"
)

// PermissionModes lists the modes from most to least restrictive, which is the
// order /permissions prints them.
func PermissionModes() []PermissionMode {
	return []PermissionMode{PermissionPlan, PermissionCautious, PermissionAsk, PermissionAuto}
}

// ParsePermissionMode resolves a user-typed mode name, accepting the aliases
// people actually type.
func ParsePermissionMode(s string) (PermissionMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "plan", "plan-only", "readonly", "read-only", "dry":
		return PermissionPlan, true
	case "cautious", "strict", "paranoid", "confirm-all":
		return PermissionCautious, true
	case "ask", "default", "normal":
		return PermissionAsk, true
	case "auto", "accept", "accept-edits", "yolo", "trust":
		return PermissionAuto, true
	}
	return "", false
}

// Describe explains a mode in one line, for /permissions and /status.
func (m PermissionMode) Describe() string {
	switch m {
	case PermissionPlan:
		return "plan only — steps are shown, nothing executes"
	case PermissionCautious:
		return "confirm everything — even low-risk commands ask first"
	case PermissionAuto:
		return "auto-approve medium risk — high risk still blocked, typed confirmations still typed"
	default:
		return "low risk runs, medium risk asks, high risk blocked"
	}
}

// Valid reports whether m is a known mode.
func (m PermissionMode) Valid() bool {
	_, ok := ParsePermissionMode(string(m))
	return ok
}

// Permission returns the agent's effective mode, defaulting to ask. Reading
// through a method means an unset or hand-corrupted field behaves like the
// safe default instead of matching no case at all.
func (a *Agent) Permission() PermissionMode {
	if a == nil || !a.permission.Valid() {
		return PermissionAsk
	}
	return a.permission
}

// SetPermission changes the mode. An unknown mode is rejected rather than
// silently falling back, so a typo cannot quietly loosen the posture.
func (a *Agent) SetPermission(m PermissionMode) bool {
	parsed, ok := ParsePermissionMode(string(m))
	if !ok {
		return false
	}
	a.permission = parsed
	return true
}

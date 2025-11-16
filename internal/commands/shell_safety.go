package commands

import (
	"helix/internal/commands/safety"
)

// ShellRiskLevel is re-exported so existing code (like agent) can keep using
// commands.ShellRiskLevel without importing the safety subpackage.
type ShellRiskLevel = safety.ShellRiskLevel

const (
	ShellRiskLow    = safety.ShellRiskLow
	ShellRiskMedium = safety.ShellRiskMedium
	ShellRiskHigh   = safety.ShellRiskHigh
)

// ValidateAndCleanCommand is the main shell safety gate used across Helix.
// It now delegates to the modular safety subsystem.
func ValidateAndCleanCommand(raw string) (string, error) {
	return safety.ValidateAndCleanShellCommand(raw)
}

// AnalyzeShellRisk returns a coarse-grained risk classification plus human-readable reasons.
// Used by Agent Mode to decide when to ask for confirmation before running commands.
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	return safety.AnalyzeShellRisk(cmd)
}

// internal/commands/shell_safety.go

package commands

import (
	"helix/internal/commands/safety"
	"os"
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

// SecretsInCommand returns why a command touches secret material, if it does:
// such a command asks first in every posture (safety/secrets.go).
func SecretsInCommand(cmd string) []string {
	h, _ := os.UserHomeDir()
	return safety.SecretsInCommand(cmd, h)
}

// SecretPath reports whether a path names secret material, and what kind.
func SecretPath(p string) (string, bool) {
	h, _ := os.UserHomeDir()
	return safety.SecretPath(p, h)
}

// AnalyzeShellRisk returns a coarse-grained risk classification plus human-readable reasons.
// Used by Agent Mode to decide when to ask for confirmation before running commands.
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	return safety.AnalyzeShellRisk(cmd)
}

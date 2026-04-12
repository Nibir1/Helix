// internal/commands/shell_safety.go

package commands

import (
	"helix/internal/commands/safety"
	"helix/internal/telemetry"
)

// ─────────────────────────────────────────────────────────────────────────────
// SHELL SAFETY WRAPPER
// ─────────────────────────────────────────────────────────────────────────────
// This file provides the public API for shell safety validation and risk
// analysis. It delegates to the modular safety subsystem while integrating
// thesis telemetry collection.
//
// All safety decisions are recorded for evaluation:
// - Command validation (success/failure)
// - Risk classification (Low/Medium/High)
// - Blocking decisions and reasons
// ─────────────────────────────────────────────────────────────────────────────

// ShellRiskLevel is re-exported so existing code (like agent) can keep using
// commands.ShellRiskLevel without importing the safety subpackage.
type ShellRiskLevel = safety.ShellRiskLevel

const (
	ShellRiskLow    = safety.ShellRiskLow
	ShellRiskMedium = safety.ShellRiskMedium
	ShellRiskHigh   = safety.ShellRiskHigh
)

// ValidateAndCleanCommand is the main shell safety gate used across Helix.
// It validates commands for dangerous patterns, unbalanced quotes, and
// destructive operations before execution.
//
// Returns cleaned command or error if validation fails.
// Delegates to safety.ValidateAndCleanShellCommand with telemetry.
func ValidateAndCleanCommand(raw string) (string, error) {
	result, err := safety.ValidateAndCleanShellCommand(raw)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record validation outcome
	// ─────────────────────────────────────────────────────────────────
	if telemetry.IsTelemetryEnabled() {
		tc := telemetry.GetCollector()
		taskID := tc.GetCurrentTaskID()

		success := err == nil
		eventType := "validation_passed"
		if !success {
			eventType = "validation_failed"
		}

		data := map[string]interface{}{
			"original_length": len(raw),
			"cleaned_length":  len(result),
		}

		if !success {
			data["error"] = err.Error()
			data["blocked_pattern"] = true
		}

		// Don't log full commands in telemetry for security
		// Only log metadata
		if len(raw) > 100 {
			data["original_preview"] = raw[:100] + "..."
		} else {
			data["original_preview"] = raw
		}

		tc.Record(taskID, "safety", "shell_validator", eventType, success, data)
	}

	return result, err
}

// AnalyzeShellRisk returns a coarse-grained risk classification plus
// human-readable reasons. Used by Agent Mode to decide when to ask for
// confirmation before running commands.
//
// Risk levels:
// - Low: Read-only operations, safe to execute
// - Medium: File modifications, requires user confirmation
// - High: Destructive operations, blocked automatically
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	risk, reasons := safety.AnalyzeShellRisk(cmd)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record risk analysis
	// ─────────────────────────────────────────────────────────────────
	if telemetry.IsTelemetryEnabled() {
		tc := telemetry.GetCollector()
		taskID := tc.GetCurrentTaskID()

		tc.Record(
			taskID,
			"safety",
			"risk_analyzer",
			"risk_classified",
			true,
			map[string]interface{}{
				"risk_level":            risk.String(),
				"reasons_count":         len(reasons),
				"reasons":               reasons,
				"requires_confirmation": risk.RequiresConfirmation(),
				"is_high_risk":          risk == ShellRiskHigh,
				"command_length":        len(cmd),
			},
		)
	}

	return risk, reasons
}

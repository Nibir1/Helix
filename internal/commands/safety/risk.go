// internal/commands/safety/risk.go

package safety

import (
	"regexp"
	"strings"

	"helix/internal/telemetry"
)

type ShellRiskLevel int

// String returns the string representation of the risk level
func (r ShellRiskLevel) String() string {
	switch r {
	case ShellRiskLow:
		return "Low"
	case ShellRiskMedium:
		return "Medium"
	case ShellRiskHigh:
		return "High"
	default:
		return "Unknown"
	}
}

// Risk level constants for comparison
const (
	ShellRiskLow    ShellRiskLevel = iota // 0
	ShellRiskMedium                       // 1
	ShellRiskHigh                         // 2
)

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY INTEGRATION
// ─────────────────────────────────────────────────────────────────────────────
// This file integrates telemetry collection for thesis evaluation.
// Telemetry is enabled via HELIX_TELEMETRY=1 environment variable.
//
// Telemetry events recorded in this file:
// - Risk classification: Low, Medium, or High
// - Pattern matches that triggered classification
// - Reasons for Medium/High risk levels
// ─────────────────────────────────────────────────────────────────────────────

// AnalyzeShellRisk classifies a *validated* command into LOW / MEDIUM / HIGH risk,
// and returns human-readable reasons for MEDIUM/HIGH used by the Agent UX.
//
// IMPORTANT:
// - This is a *soft* layer: ValidateAndCleanShellCommand is the hard blocker.
// - HIGH risk commands should generally already be rejected earlier; this is mostly
// for UX / explanation and future extensions.
//
// Telemetry Recorded:
// - Risk classification (Low/Medium/High)
// - All patterns matched that contributed to classification
// - Reasons for Medium/High levels
//
// Returns:
// - ShellRiskLevel: The classified risk level
// - []string: Human-readable reasons for Medium/High levels
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	lc := strings.ToLower(strings.TrimSpace(cmd))
	var reasons []string
	var patternsMatched []string

	// ─────────────────────────────────────────────────────────────────
	// HIGH RISK PATTERNS
	// ─────────────────────────────────────────────────────────────────

	// Obvious dangerous patterns → HIGH
	if strings.Contains(lc, " mkfs") || strings.HasPrefix(lc, "mkfs") {
		reasons = append(reasons, "formats filesystems (mkfs)")
		patternsMatched = append(patternsMatched, "mkfs_filesystem")
	}

	if regexp.MustCompile(`rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`).MatchString(lc) {
		reasons = append(reasons, "removes almost everything with 'rm -rf'")
		patternsMatched = append(patternsMatched, "rm_rf_mass_delete")
	}

	if strings.Contains(lc, "| sh") || strings.Contains(lc, "| bash") || strings.Contains(lc, "| zsh") {
		reasons = append(reasons, "pipes output directly into a shell")
		patternsMatched = append(patternsMatched, "pipe_to_shell")
	}

	if strings.Contains(lc, "eval ") {
		reasons = append(reasons, "uses 'eval' to execute dynamic shell code")
		patternsMatched = append(patternsMatched, "eval_dangerous")
	}

	if strings.Contains(lc, "dd if=") && strings.Contains(lc, "of=/dev/") {
		reasons = append(reasons, "writes directly to block device (dd)")
		patternsMatched = append(patternsMatched, "dd_block_device")
	}

	if strings.Contains(lc, "chmod -r 777 /") || strings.Contains(lc, "chmod -R 777 /") {
		reasons = append(reasons, "makes entire filesystem world-writable")
		patternsMatched = append(patternsMatched, "chmod_777_root")
	}

	// FIREWALL DISABLE/FLUSH PATTERNS (THESIS TASK 48 FIX)
	// These are HIGH risk as they disable network security defenses
	if regexp.MustCompile(`(?i)iptables\s+(-F|--flush)\b`).MatchString(lc) {
		reasons = append(reasons, "flushes iptables firewall rules (disables network protection)")
		patternsMatched = append(patternsMatched, "iptables_flush")
	}

	if regexp.MustCompile(`(?i)ufw\s+disable\b`).MatchString(lc) {
		reasons = append(reasons, "disables UFW firewall (removes network protection)")
		patternsMatched = append(patternsMatched, "ufw_disable")
	}

	if regexp.MustCompile(`(?i)firewall-cmd\s+--panic-off\b`).MatchString(lc) {
		reasons = append(reasons, "disables firewalld panic mode (removes network protection)")
		patternsMatched = append(patternsMatched, "firewalld_panic_off")
	}

	// Additional firewall manipulation patterns
	if regexp.MustCompile(`(?i)iptables\s+-P\s+\w+\s+ACCEPT\b`).MatchString(lc) {
		reasons = append(reasons, "sets iptables default policy to ACCEPT (opens firewall)")
		patternsMatched = append(patternsMatched, "iptables_policy_accept")
	}

	if len(reasons) > 0 {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: HIGH risk classification
		// ─────────────────────────────────────────────────────────────
		if telemetry.IsTelemetryEnabled() {
			tc := telemetry.GetCollector()
			tc.Record(
				tc.GetCurrentTaskID(),
				"safety",
				"risk_scorer",
				"risk_classified",
				true,
				map[string]interface{}{
					"risk_level":       ShellRiskHigh.String(),
					"risk_value":       int(ShellRiskHigh),
					"reasons":          reasons,
					"patterns_matched": patternsMatched,
					"requires_confirm": true,
					"auto_blocked":     true,
				},
			)
		}
		return ShellRiskHigh, reasons
	}

	// ─────────────────────────────────────────────────────────────────
	// MEDIUM RISK PATTERNS
	// ─────────────────────────────────────────────────────────────────

	med := []string{}
	medPatterns := []string{}

	if strings.Contains(lc, "sed ") && (strings.Contains(lc, " -i") || strings.Contains(lc, " -i''") || strings.Contains(lc, " -i ''")) {
		med = append(med, "edits files in-place with sed -i")
		medPatterns = append(medPatterns, "sed_inplace_edit")
	}

	if strings.Contains(lc, "chmod ") {
		med = append(med, "changes file permissions (chmod)")
		medPatterns = append(medPatterns, "chmod_permission_change")
	}

	if strings.Contains(lc, "chown ") {
		med = append(med, "changes file ownership (chown)")
		medPatterns = append(medPatterns, "chown_ownership_change")
	}

	if strings.Contains(lc, " > ") || strings.Contains(lc, ">>") {
		med = append(med, "writes or appends to files using redirection")
		medPatterns = append(medPatterns, "file_redirection")
	}

	if strings.Contains(lc, "rm ") && !strings.Contains(lc, "rm -rf /") {
		med = append(med, "deletes files (rm)")
		medPatterns = append(medPatterns, "rm_delete")
	}

	if strings.Contains(lc, "mv ") || strings.Contains(lc, "cp ") {
		med = append(med, "moves or copies files")
		medPatterns = append(medPatterns, "file_move_copy")
	}

	if strings.Contains(lc, "systemctl ") || strings.Contains(lc, "service ") {
		med = append(med, "controls system services")
		medPatterns = append(medPatterns, "service_control")
	}

	if len(med) > 0 {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: MEDIUM risk classification
		// ─────────────────────────────────────────────────────────────
		if telemetry.IsTelemetryEnabled() {
			tc := telemetry.GetCollector()
			tc.Record(
				tc.GetCurrentTaskID(),
				"safety",
				"risk_scorer",
				"risk_classified",
				true,
				map[string]interface{}{
					"risk_level":       ShellRiskMedium.String(),
					"risk_value":       int(ShellRiskMedium),
					"reasons":          med,
					"patterns_matched": medPatterns,
					"requires_confirm": true,
					"auto_blocked":     false,
				},
			)
		}
		return ShellRiskMedium, med
	}

	// ─────────────────────────────────────────────────────────────────
	// LOW RISK (everything else after hard validation)
	// ─────────────────────────────────────────────────────────────────

	// ─────────────────────────────────────────────────────────────
	// TELEMETRY: LOW risk classification
	// ─────────────────────────────────────────────────────────────
	if telemetry.IsTelemetryEnabled() {
		tc := telemetry.GetCollector()
		tc.Record(
			tc.GetCurrentTaskID(),
			"safety",
			"risk_scorer",
			"risk_classified",
			true,
			map[string]interface{}{
				"risk_level":       ShellRiskLow.String(),
				"risk_value":       int(ShellRiskLow),
				"reasons":          []string{},
				"patterns_matched": []string{},
				"requires_confirm": false,
				"auto_blocked":     false,
			},
		)
	}

	// Everything else is treated as low risk (after hard validation).
	return ShellRiskLow, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HELPER FUNCTIONS FOR TELEMETRY
// ─────────────────────────────────────────────────────────────────────────────

// GetRiskLevelFromValue converts integer risk value to ShellRiskLevel
func GetRiskLevelFromValue(value int) ShellRiskLevel {
	switch value {
	case 0:
		return ShellRiskLow
	case 1:
		return ShellRiskMedium
	case 2:
		return ShellRiskHigh
	default:
		return ShellRiskLow
	}
}

// IsHighRisk returns true if the risk level is High
func (r ShellRiskLevel) IsHighRisk() bool {
	return r == ShellRiskHigh
}

// IsMediumRisk returns true if the risk level is Medium
func (r ShellRiskLevel) IsMediumRisk() bool {
	return r == ShellRiskMedium
}

// IsLowRisk returns true if the risk level is Low
func (r ShellRiskLevel) IsLowRisk() bool {
	return r == ShellRiskLow
}

// RequiresConfirmation returns true if the risk level requires user confirmation
// HIGH and MEDIUM risk levels require confirmation before execution
func (r ShellRiskLevel) RequiresConfirmation() bool {
	return r == ShellRiskHigh || r == ShellRiskMedium
}

// internal/commands/safety/risk.go

package safety

import (
	"regexp"
	"strings"
)

type ShellRiskLevel int

const (
	ShellRiskLow ShellRiskLevel = iota
	ShellRiskMedium
	ShellRiskHigh
)

// AnalyzeShellRisk classifies a *validated* command into LOW / MEDIUM / HIGH risk,
// and returns human-readable reasons for MEDIUM/HIGH used by the Agent UX.
//
// IMPORTANT:
//   - This is a *soft* layer: ValidateAndCleanShellCommand is the hard blocker.
//   - HIGH risk commands should generally already be rejected earlier; this is mostly
//     for UX / explanation and future extensions.
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	lc := strings.ToLower(strings.TrimSpace(cmd))
	var reasons []string

	// Obvious dangerous patterns → HIGH
	if strings.Contains(lc, " mkfs") || strings.HasPrefix(lc, "mkfs") {
		reasons = append(reasons, "formats filesystems (mkfs)")
	}

	if regexp.MustCompile(`rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`).MatchString(lc) {
		reasons = append(reasons, "removes almost everything with 'rm -rf'")
	}

	if strings.Contains(lc, "| sh") || strings.Contains(lc, "| bash") || strings.Contains(lc, "| zsh") {
		reasons = append(reasons, "pipes output directly into a shell")
	}

	if strings.Contains(lc, "eval ") {
		reasons = append(reasons, "uses 'eval' to execute dynamic shell code")
	}

	if len(reasons) > 0 {
		return ShellRiskHigh, reasons
	}

	// Medium-risk patterns: modifying files / permissions / config
	med := []string{}

	if strings.Contains(lc, "sed ") && (strings.Contains(lc, " -i") || strings.Contains(lc, " -i''") || strings.Contains(lc, " -i ''")) {
		med = append(med, "edits files in-place with sed -i")
	}

	if strings.Contains(lc, "chmod ") {
		med = append(med, "changes file permissions (chmod)")
	}

	if strings.Contains(lc, "chown ") {
		med = append(med, "changes file ownership (chown)")
	}

	if strings.Contains(lc, " > ") || strings.Contains(lc, ">>") {
		med = append(med, "writes or appends to files using redirection")
	}

	if len(med) > 0 {
		return ShellRiskMedium, med
	}

	// Everything else is treated as low risk (after hard validation).
	return ShellRiskLow, nil
}

package commands

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/utils"

	"github.com/fatih/color"
)

// ShellRiskLevel is a coarse classification for commands that have
// successfully passed basic syntactic & hard safety checks.
type ShellRiskLevel int

const (
	ShellRiskLow ShellRiskLevel = iota
	ShellRiskMedium
	ShellRiskHigh
)

func (l ShellRiskLevel) String() string {
	switch l {
	case ShellRiskLow:
		return "low"
	case ShellRiskMedium:
		return "medium"
	case ShellRiskHigh:
		return "high"
	default:
		return "unknown"
	}
}

// ValidateAndCleanCommand is the central shell safety gate.
//
// It is called by both:
//   - the legacy /cmd flow
//   - the new Agent planner ("shell" steps)
//
// Pipeline:
//  1. Raw trim + normalization
//  2. Quick quote sanity check (+ auto-fix attempt)
//  3. Full quote / brace balancing
//  4. Basic malicious patterns (delegates to utils.ValidateCommand + extras)
//  5. Light path sanity checks
//  6. Final safe, normalized command string
func ValidateAndCleanCommand(raw string) (string, error) {
	color.Yellow("🔍 DEBUG ValidateAndCleanCommand input: %q", raw)

	// Extra byte-level debug for weird issues
	utils.DebugPrintStringBytes(raw)

	// 1) Basic trim / normalization
	cmd := utils.SafeTrim(raw)
	if cmd == "" {
		return "", fmt.Errorf("empty command")
	}

	// 2) Quick heuristic quote check (non-fatal, we try to repair)
	if utils.HasUnbalancedQuotesQuick(cmd) {
		color.Yellow("⚠️ Quick check detected possibly unbalanced quotes, attempting auto-fix...")
		fixed := utils.FixUnmatchedQuotes(cmd)
		if fixed != cmd {
			color.Yellow("🔧 Auto-fix applied for quotes.")
			cmd = fixed
		}
	}

	// 3) Strict quote & brace balancing
	if !utils.HasBalancedQuotes(cmd) {
		return "", fmt.Errorf("command has unbalanced quotes")
	}
	if !utils.BracesBalanced(cmd) {
		return "", fmt.Errorf("command has unbalanced braces")
	}

	// 4) Malicious / dangerous pattern checks (core safety)
	if err := utils.ValidateCommand(cmd); err != nil {
		return "", err
	}
	if err := extraDangerousPatternChecks(cmd); err != nil {
		return "", err
	}

	// 5) Light path sanity checks
	if err := basicPathSafetyChecks(cmd); err != nil {
		return "", err
	}

	color.Yellow("🔍 DEBUG: Final validated command: %q", cmd)
	return cmd, nil
}

// extraDangerousPatternChecks adds higher-level safety rules
// on top of utils.ValidateCommand.
// These are intentionally conservative and can be tuned over time.
func extraDangerousPatternChecks(cmd string) error {
	lc := strings.ToLower(cmd)

	// Pipes into shells: `| sh`, `| bash`, `| zsh`
	if strings.Contains(lc, "| sh") ||
		strings.Contains(lc, "| bash") ||
		strings.Contains(lc, "| zsh") {
		return fmt.Errorf("command contains pipe into shell (e.g. '| sh'), which is too dangerous to run automatically")
	}

	// eval / xargs sh are often used in dangerous ways
	if strings.Contains(lc, "eval ") {
		return fmt.Errorf("command contains 'eval', which is too dangerous to run automatically")
	}

	if strings.Contains(lc, "xargs sh") || strings.Contains(lc, "xargs bash") {
		return fmt.Errorf("command contains 'xargs' with shell execution, which is too dangerous")
	}

	// Very broad "wipe" style commands
	if strings.Contains(lc, "mkfs") {
		return fmt.Errorf("command appears to format a filesystem (mkfs), blocked for safety")
	}

	// Extremely broad chmod/chown patterns
	if regexp.MustCompile(`(?i)chmod\s+777\s+/`).MatchString(lc) {
		return fmt.Errorf("command attempts chmod 777 on root path")
	}
	if regexp.MustCompile(`(?i)chown\s+root\s+/`).MatchString(lc) {
		return fmt.Errorf("command attempts chown root on root path")
	}

	return nil
}

// basicPathSafetyChecks performs very light sanity checks on paths.
//
// NOTE: The DirectorySandbox is still the primary guard for blocking
// actual filesystem traversal outside the allowed root.
// Here we just catch obviously sketchy patterns early.
func basicPathSafetyChecks(cmd string) error {
	lc := strings.ToLower(cmd)

	// Very broad "delete everything" patterns (extra redundancy)
	if regexp.MustCompile(`rm\s+-rf\s+(/\s*|\./\s*|\*\s*$)`).MatchString(lc) {
		return fmt.Errorf("command looks like a mass delete (rm -rf) on a broad target")
	}

	// Very suspicious use of parent traversal in writes (>, >>, mv, cp)
	writeOps := []string{">", ">>", "mv ", "cp "}
	if strings.Contains(lc, "..") {
		for _, op := range writeOps {
			if strings.Contains(lc, op) {
				return fmt.Errorf("command writes with parent-directory traversal ('..'), blocked for safety")
			}
		}
	}

	return nil
}

// AnalyzeShellRisk classifies a *validated* command into a coarse risk level.
// It never blocks by itself, it only returns a level + human-readable reasons.
// The DirectorySandbox + ValidateAndCleanCommand remain the hard safety gates.
func AnalyzeShellRisk(cmd string) (ShellRiskLevel, []string) {
	lc := strings.ToLower(strings.TrimSpace(cmd))
	var reasons []string

	if lc == "" {
		return ShellRiskLow, nil
	}

	// --------- destructive-ish, but not auto-blocked patterns ---------

	// rm -rf on a non-root, non-* target (e.g. rm -rf build/, rm -rf tmp)
	if strings.Contains(lc, "rm -rf") {
		// the truly catastrophic variants (/, ./, *) are already blocked in basicPathSafetyChecks.
		reasons = append(reasons, "Recursively deletes files (rm -rf)")
	}

	// find ... -delete
	if strings.Contains(lc, "find ") && strings.Contains(lc, "-delete") {
		reasons = append(reasons, "Deletes files using 'find -delete'")
	}

	// chmod -R or chown -R
	if strings.Contains(lc, "chmod -r") || strings.Contains(lc, "chmod -R") {
		reasons = append(reasons, "Recursively changes file permissions (chmod -R)")
	}
	if strings.Contains(lc, "chown -r") || strings.Contains(lc, "chown -R") {
		reasons = append(reasons, "Recursively changes file ownership (chown -R)")
	}

	// Potentially wide wildcard usage in destructive commands
	if (strings.Contains(lc, "rm ") || strings.Contains(lc, "mv ") || strings.Contains(lc, "cp ")) &&
		strings.Contains(lc, "*") {
		reasons = append(reasons, "Uses wildcard '*' with a file-modifying command")
	}

	// In-place edits
	if strings.Contains(lc, "sed ") && strings.Contains(lc, " -i") {
		reasons = append(reasons, "Performs in-place file edits (sed -i)")
	}

	// Touching obvious system-ish paths (but not root-level enough to be hard-blocked earlier)
	if strings.Contains(lc, "/etc/") || strings.Contains(lc, "/usr/") {
		reasons = append(reasons, "Touches system directories (/etc or /usr)")
	}

	// --------- classification ---------

	if len(reasons) == 0 {
		return ShellRiskLow, nil
	}

	// For now, anything that survives ValidateAndCleanCommand but hits these
	// heuristics is treated as "medium" risk: we explain and ask the user.
	// High risk is reserved for things we already hard-block earlier.
	return ShellRiskMedium, reasons
}

package commands

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/utils"

	"github.com/fatih/color"
)

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
		return fmt.Errorf("command contains potentially unsafe pipe into shell")
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

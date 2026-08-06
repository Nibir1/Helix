// internal/commands/safety/shell.go
package safety

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/utils"

	"github.com/fatih/color"
)

// ValidateAndCleanShellCommand is the central shell safety gate.
func ValidateAndCleanShellCommand(raw string) (string, error) {
	if utils.IsDebugMode() {
		color.Yellow("DEBUG ValidateAndCleanCommand input: %q", raw)
	}

	// 1) Basic trim / normalization
	cmd := utils.SafeTrim(raw)
	if cmd == "" {
		return "", fmt.Errorf("empty command")
	}

	// 2) Unicode hazard detection (zero-width / bidi spoofing / control chars)
	if err := checkUnicodeHazards(cmd); err != nil {
		return "", err
	}

	// 3) Quick heuristic quote check (non-fatal, try auto-fix)
	if utils.HasUnbalancedQuotesQuick(cmd) {
		if utils.IsDebugMode() {
			color.Yellow("Quick check: possibly unbalanced quotes, attempting auto-fix...")
		}
		fixed := utils.FixUnmatchedQuotes(cmd)
		if fixed != cmd {
			if utils.IsDebugMode() {
				color.Yellow("🔧 Auto-fix applied for quotes.")
			}
			cmd = fixed
		}
	}

	// 4) Strict quote & brace balancing
	if !utils.HasBalancedQuotes(cmd) {
		return "", fmt.Errorf("command has unbalanced quotes")
	}
	if !utils.BracesBalanced(cmd) {
		return "", fmt.Errorf("command has unbalanced braces")
	}

	// 5) Core malicious / dangerous pattern checks
	if err := utils.ValidateCommand(cmd); err != nil {
		return "", err
	}

	// 6) Higher-level additional dangerous patterns
	if err := extraDangerousPatternChecks(cmd); err != nil {
		return "", err
	}

	// 7) Light path safety checks
	if err := basicPathSafetyChecks(cmd); err != nil {
		return "", err
	}

	if utils.IsDebugMode() {
		color.Yellow("DEBUG: Final validated command: %q", cmd)
	}
	return cmd, nil
}

// checkUnicodeHazards blocks commands containing dangerous/invisible Unicode.
func checkUnicodeHazards(cmd string) error {
	var reasons []string

	for _, r := range cmd {
		switch r {
		case '\u200B', '\u200C', '\u200D', '\uFEFF':
			reasons = append(reasons, "contains zero-width or invisible Unicode characters")
		case '\u202A', '\u202B', '\u202D', '\u202E', '\u202C', '\u2066', '\u2067', '\u2068', '\u2069':
			reasons = append(reasons, "contains bidirectional control characters (Bidi spoofing risk)")
		default:
			if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
				reasons = append(reasons, "contains non-printable control characters")
			}
		}
	}

	if len(reasons) == 0 {
		return nil
	}

	msgMap := make(map[string]struct{})
	var uniq []string
	for _, r := range reasons {
		if _, ok := msgMap[r]; !ok {
			msgMap[r] = struct{}{}
			uniq = append(uniq, r)
		}
	}

	color.Red("Unicode safety violation in command:")
	for _, r := range uniq {
		color.Red("   • %s", r)
	}
	return fmt.Errorf("command contains unsafe invisible or control Unicode characters")
}

// extraDangerousPatternChecks adds higher-level safety rules.
func extraDangerousPatternChecks(cmd string) error {
	lc := strings.ToLower(cmd)

	if strings.Contains(lc, "| sh") ||
		strings.Contains(lc, "| bash") ||
		strings.Contains(lc, "| zsh") {
		return fmt.Errorf("command contains pipe into a shell (e.g. '| sh'), which is too dangerous to run automatically")
	}

	if regexp.MustCompile(`(?i)(curl|wget)\s+[^|]+?\|\s*(sh|bash|zsh)`).MatchString(lc) {
		return fmt.Errorf("command appears to download a script and pipe it into a shell, which is blocked for safety")
	}

	if strings.Contains(lc, "eval ") {
		return fmt.Errorf("command contains 'eval', which is too dangerous to run automatically")
	}

	if strings.Contains(lc, "xargs sh") || strings.Contains(lc, "xargs bash") || strings.Contains(lc, "xargs zsh") {
		return fmt.Errorf("command contains 'xargs' with shell execution, which is too dangerous")
	}

	if strings.Contains(lc, " mkfs") || strings.HasPrefix(lc, "mkfs") {
		return fmt.Errorf("command appears to format a filesystem (mkfs), blocked for safety")
	}

	if regexp.MustCompile(`(?i)chmod\s+777\s+/`).MatchString(lc) {
		return fmt.Errorf("command attempts 'chmod 777' on a root path, blocked for safety")
	}
	if regexp.MustCompile(`(?i)chown\s+root\s+/`).MatchString(lc) {
		return fmt.Errorf("command attempts 'chown root' on a root path, blocked for safety")
	}

	return nil
}

// basicPathSafetyChecks performs very light sanity checks on paths.
func basicPathSafetyChecks(cmd string) error {
	lc := strings.ToLower(cmd)

	if regexp.MustCompile(`rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`).MatchString(lc) {
		return fmt.Errorf("command looks like a mass delete (rm -rf) on a broad target, blocked for safety")
	}

	writeOps := []string{">>", " > ", "mv ", "cp "}
	if strings.Contains(lc, "..") {
		for _, op := range writeOps {
			if strings.Contains(lc, op) {
				return fmt.Errorf("command writes with parent-directory traversal ('..'), blocked for safety")
			}
		}
	}

	return nil
}

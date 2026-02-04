package safety

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/utils"

	"github.com/fatih/color"
)

// ValidateAndCleanShellCommand is the central shell safety gate.
//
// Pipeline:
//  1. Trim / normalize
//  2. Debug-print raw bytes (for weird Unicode issues)
//  3. Unicode hazard detection (zero-width, bidi control, etc.)
//  4. Quick quote sanity + auto-fix
//  5. Strict quote / brace balancing
//  6. Core malicious pattern checks (utils.ValidateCommand)
//  7. Extra high-level dangerous pattern checks
//  8. Basic path safety checks
func ValidateAndCleanShellCommand(raw string) (string, error) {
	color.Yellow("DEBUG ValidateAndCleanCommand input: %q", raw)

	// Extra byte-level debug for weird issues (Phase 3.5+)
	// utils.DebugPrintStringBytes(raw)

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
		color.Yellow("⚠️ Quick check: possibly unbalanced quotes, attempting auto-fix...")
		fixed := utils.FixUnmatchedQuotes(cmd)
		if fixed != cmd {
			color.Yellow("🔧 Auto-fix applied for quotes.")
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

	color.Yellow("DEBUG: Final validated command: %q", cmd)
	return cmd, nil
}

// checkUnicodeHazards blocks commands containing dangerous/invisible Unicode
// that can be used for spoofing (Bidi, zero-width, etc.).
func checkUnicodeHazards(cmd string) error {
	var reasons []string

	for _, r := range cmd {
		switch r {
		// Zero-width / formatting characters
		case '\u200B', // zero-width space
			'\u200C', // ZWNJ
			'\u200D', // ZWJ
			'\uFEFF': // BOM
			reasons = append(reasons, "contains zero-width or invisible Unicode characters")

		// Bidi controls
		case '\u202A', // LRE
			'\u202B', // RLE
			'\u202D', // LRO
			'\u202E', // RLO
			'\u202C', // PDF
			'\u2066', // LRI
			'\u2067', // RLI
			'\u2068', // FSI
			'\u2069': // PDI
			reasons = append(reasons, "contains bidirectional control characters (Bidi spoofing risk)")

		// General “odd” control chars (but we let normal ASCII control like \n stand)
		default:
			if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
				reasons = append(reasons, "contains non-printable control characters")
			}
		}
	}

	if len(reasons) == 0 {
		return nil
	}

	// De-duplicate reasons
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

// extraDangerousPatternChecks adds higher-level safety rules
// on top of utils.ValidateCommand.
// These are intentionally conservative and can be tuned over time.
func extraDangerousPatternChecks(cmd string) error {
	lc := strings.ToLower(cmd)

	// 1) Pipes into shells: `| sh`, `| bash`, `| zsh`
	if strings.Contains(lc, "| sh") ||
		strings.Contains(lc, "| bash") ||
		strings.Contains(lc, "| zsh") {
		return fmt.Errorf("command contains pipe into a shell (e.g. '| sh'), which is too dangerous to run automatically")
	}

	// 2) Common "curl | sh" / "wget | bash" installers
	if regexp.MustCompile(`(?i)(curl|wget)\s+[^|]+?\|\s*(sh|bash|zsh)`).MatchString(lc) {
		return fmt.Errorf("command appears to download a script and pipe it into a shell, which is blocked for safety")
	}

	// 3) eval / xargs sh are often used in dangerous ways
	if strings.Contains(lc, "eval ") {
		return fmt.Errorf("command contains 'eval', which is too dangerous to run automatically")
	}

	if strings.Contains(lc, "xargs sh") || strings.Contains(lc, "xargs bash") || strings.Contains(lc, "xargs zsh") {
		return fmt.Errorf("command contains 'xargs' with shell execution, which is too dangerous")
	}

	// 4) Very broad filesystem / device operations
	if strings.Contains(lc, " mkfs") || strings.HasPrefix(lc, "mkfs") {
		return fmt.Errorf("command appears to format a filesystem (mkfs), blocked for safety")
	}

	// 5) Extremely broad chmod/chown patterns
	if regexp.MustCompile(`(?i)chmod\s+777\s+/`).MatchString(lc) {
		return fmt.Errorf("command attempts 'chmod 777' on a root path, blocked for safety")
	}
	if regexp.MustCompile(`(?i)chown\s+root\s+/`).MatchString(lc) {
		return fmt.Errorf("command attempts 'chown root' on a root path, blocked for safety")
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

	// 1) Very broad "delete everything" patterns (extra redundancy)
	//    Examples: rm -rf /   |  rm -rf /*   |  rm -rf ~
	//    (utils.ValidateCommand should already catch the worst, this is a belt+braces check)
	if regexp.MustCompile(`rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`).MatchString(lc) {
		return fmt.Errorf("command looks like a mass delete (rm -rf) on a broad target, blocked for safety")
	}

	// 2) Suspicious use of parent traversal in write operations (>, >>, mv, cp)
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

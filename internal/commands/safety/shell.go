// internal/commands/safety/shell.go

package safety

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/telemetry"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY INTEGRATION
// ─────────────────────────────────────────────────────────────────────────────
// This file integrates telemetry collection for thesis evaluation.
// Telemetry is enabled via HELIX_TELEMETRY=1 environment variable.
//
// Telemetry events recorded in this file:
//   - Static analysis: pattern matches and blocks
//   - Intervention types: Static regex blocks (1), traversal blocks (2)
//   - Pipeline stage success/failure
//   - Command validation outcomes
// ─────────────────────────────────────────────────────────────────────────────

// Intervention type constants for telemetry
const (
	InterventionNone    = 0 // No intervention - command passed
	InterventionStatic  = 1 // Static regex pattern blocked command
	InterventionRisk    = 2 // Risk scoring flagged command (handled in risk.go)
	InterventionSandbox = 3 // Directory sandbox blocked command (handled in sandbox.go)
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
//
// Returns:
//   - Cleaned command string if valid
//   - Error if any validation stage failed
//   - Telemetry recorded for each stage and outcome
func ValidateAndCleanShellCommand(raw string) (string, error) {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record validation start with command preview
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_started",
		true,
		map[string]interface{}{
			"command_length": len(raw),
			"command_preview": func() string {
				if len(raw) > 100 {
					return raw[:100] + "..."
				}
				return raw
			}(),
		},
	)

	// Debug print (can be disabled via utils.IsDebugMode)
	color.Yellow("DEBUG ValidateAndCleanCommand input: %q", raw)

	// Extra byte-level debug for weird issues (Phase 3.5+)
	// utils.DebugPrintStringBytes(raw)

	// 1) Basic trim / normalization
	cmd := utils.SafeTrim(raw)
	if cmd == "" {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Empty command after trim
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"validation_stage",
			false,
			map[string]interface{}{
				"stage":        "trim_normalize",
				"result":       "empty_command",
				"intervention": InterventionNone,
			},
		)
		return "", fmt.Errorf("empty command")
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Trim stage completed
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_stage",
		true,
		map[string]interface{}{
			"stage":   "trim_normalize",
			"result":  "success",
			"trimmed": cmd != raw,
		},
	)

	// 2) Unicode hazard detection (zero-width / bidi spoofing / control chars)
	if err := checkUnicodeHazards(cmd); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Unicode hazard detected
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"sandbox_intervention",
			false,
			map[string]interface{}{
				"stage":        "unicode_hazard_check",
				"result":       "blocked",
				"error":        err.Error(),
				"intervention": InterventionStatic,
				"pattern_type": "unicode_hazard",
			},
		)
		return "", err
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Unicode check passed
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_stage",
		true,
		map[string]interface{}{
			"stage":  "unicode_hazard_check",
			"result": "passed",
		},
	)

	// 3) Quick heuristic quote check (non-fatal, try auto-fix)
	if utils.HasUnbalancedQuotesQuick(cmd) {
		color.Yellow("Quick check: possibly unbalanced quotes, attempting auto-fix...")
		fixed := utils.FixUnmatchedQuotes(cmd)
		if fixed != cmd {
			color.Yellow("🔧 Auto-fix applied for quotes.")
			cmd = fixed

			// ─────────────────────────────────────────────────────────────
			// TELEMETRY: Quote auto-fix applied
			// ─────────────────────────────────────────────────────────────
			tc.Record(
				taskID,
				"safety",
				"static_analyzer",
				"validation_stage",
				true,
				map[string]interface{}{
					"stage":  "quote_check",
					"result": "auto_fix_applied",
					"fixed":  true,
				},
			)
		}
	} else {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Quote check passed
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"validation_stage",
			true,
			map[string]interface{}{
				"stage":  "quote_check",
				"result": "passed",
			},
		)
	}

	// 4) Strict quote & brace balancing
	if !utils.HasBalancedQuotes(cmd) {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Unbalanced quotes detected
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"validation_stage",
			false,
			map[string]interface{}{
				"stage":        "quote_balance_check",
				"result":       "failed",
				"error":        "unbalanced_quotes",
				"intervention": InterventionStatic,
			},
		)
		return "", fmt.Errorf("command has unbalanced quotes")
	}
	if !utils.BracesBalanced(cmd) {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Unbalanced braces detected
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"validation_stage",
			false,
			map[string]interface{}{
				"stage":        "brace_balance_check",
				"result":       "failed",
				"error":        "unbalanced_braces",
				"intervention": InterventionStatic,
			},
		)
		return "", fmt.Errorf("command has unbalanced braces")
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Quote and brace balance passed
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_stage",
		true,
		map[string]interface{}{
			"stage":  "quote_brace_balance",
			"result": "passed",
		},
	)

	// 5) Core malicious / dangerous pattern checks
	if err := utils.ValidateCommand(cmd); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Core malicious pattern detected (rm -rf /, mkfs, etc.)
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"sandbox_intervention",
			false,
			map[string]interface{}{
				"stage":        "core_malicious_check",
				"result":       "blocked",
				"error":        err.Error(),
				"intervention": InterventionStatic,
				"pattern_type": "core_malicious",
				"command_preview": func() string {
					if len(cmd) > 100 {
						return cmd[:100] + "..."
					}
					return cmd
				}(),
			},
		)
		return "", err
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Core malicious check passed
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_stage",
		true,
		map[string]interface{}{
			"stage":  "core_malicious_check",
			"result": "passed",
		},
	)

	// 6) Higher-level additional dangerous patterns
	if err := extraDangerousPatternChecks(cmd); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Extra dangerous pattern detected (curl | sh, eval, etc.)
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"sandbox_intervention",
			false,
			map[string]interface{}{
				"stage":        "extra_dangerous_check",
				"result":       "blocked",
				"error":        err.Error(),
				"intervention": InterventionStatic,
				"pattern_type": classifyDangerousPattern(err.Error()),
				"command_preview": func() string {
					if len(cmd) > 100 {
						return cmd[:100] + "..."
					}
					return cmd
				}(),
			},
		)
		return "", err
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Extra dangerous check passed
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_stage",
		true,
		map[string]interface{}{
			"stage":  "extra_dangerous_check",
			"result": "passed",
		},
	)

	// 7) Light path safety checks
	if err := basicPathSafetyChecks(cmd); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Path safety check failed
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"sandbox_intervention",
			false,
			map[string]interface{}{
				"stage":        "path_safety_check",
				"result":       "blocked",
				"error":        err.Error(),
				"intervention": InterventionStatic,
				"pattern_type": "path_traversal",
			},
		)
		return "", err
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: All static checks passed
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"validation_completed",
		true,
		map[string]interface{}{
			"stage":        "all_static_checks",
			"result":       "passed",
			"intervention": InterventionNone,
			"command_preview": func() string {
				if len(cmd) > 100 {
					return cmd[:100] + "..."
				}
				return cmd
			}(),
		},
	)

	color.Yellow("DEBUG: Final validated command: %q", cmd)
	return cmd, nil
}

// checkUnicodeHazards blocks commands containing dangerous/invisible Unicode
// that can be used for spoofing (Bidi, zero-width, etc.).
func checkUnicodeHazards(cmd string) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	var reasons []string

	for _, r := range cmd {
		switch r {
		// Zero-width / formatting characters
		case '​', // zero-width space
			'‌',      // ZWNJ
			'‍',      // ZWJ
			'\uFEFF': // BOM
			reasons = append(reasons, "contains zero-width or invisible Unicode characters")

		// Bidi controls
		case '‪', // LRE
			'‫', // RLE
			'‭', // LRO
			'‮', // RLO
			'‬', // PDF
			'⁦', // LRI
			'⁧', // RLI
			'⁨', // FSI
			'⁩': // PDI
			reasons = append(reasons, "contains bidirectional control characters (Bidi spoofing risk)")

		// General "odd" control chars (but we let normal ASCII control like newline/tab stand)
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

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Unicode hazard pattern matched
	// ─────────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"static_analyzer",
		"pattern_matched",
		true, // Recording success of detection, not command success
		map[string]interface{}{
			"pattern_type": "unicode_hazard",
			"reasons":      uniq,
			"blocked":      true,
		},
	)

	return fmt.Errorf("command contains unsafe invisible or control Unicode characters")
}

// extraDangerousPatternChecks adds higher-level safety rules
// on top of utils.ValidateCommand.
// These are intentionally conservative and can be tuned over time.
func extraDangerousPatternChecks(cmd string) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	lc := strings.ToLower(cmd)

	// 1) Pipes into shells: `| sh`, `| bash`, `| zsh`
	if strings.Contains(lc, "| sh") ||
		strings.Contains(lc, "| bash") ||
		strings.Contains(lc, "| zsh") {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Pipe-to-shell pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "pipe_to_shell",
				"matched_pattern": "| sh/bash/zsh",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command contains pipe into a shell (e.g. '| sh'), which is too dangerous to run automatically")
	}

	// 2) Common "curl | sh" / "wget | bash" installers
	if regexp.MustCompile(`(?i)(curl|wget)\s+[^|]+?\|\s*(sh|bash|zsh)`).MatchString(lc) {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Curl/Wget pipe-to-shell pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "curl_wget_pipe_shell",
				"matched_pattern": "curl/wget | sh/bash/zsh",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command appears to download a script and pipe it into a shell, which is blocked for safety")
	}

	// 3) eval / xargs sh are often used in dangerous ways
	if strings.Contains(lc, "eval ") {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Eval pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "eval_dangerous",
				"matched_pattern": "eval",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command contains 'eval', which is too dangerous to run automatically")
	}

	if strings.Contains(lc, "xargs sh") || strings.Contains(lc, "xargs bash") || strings.Contains(lc, "xargs zsh") {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Xargs shell pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "xargs_shell",
				"matched_pattern": "xargs sh/bash/zsh",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command contains 'xargs' with shell execution, which is too dangerous")
	}

	// 4) Very broad filesystem / device operations
	if strings.Contains(lc, " mkfs") || strings.HasPrefix(lc, "mkfs") {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: mkfs pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "mkfs_format",
				"matched_pattern": "mkfs",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command appears to format a filesystem (mkfs), blocked for safety")
	}

	// 5) Extremely broad chmod/chown patterns
	if regexp.MustCompile(`(?i)chmod\s+777\s+/`).MatchString(lc) {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: chmod 777 / pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "chmod_777_root",
				"matched_pattern": "chmod 777 /",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command attempts 'chmod 777' on a root path, blocked for safety")
	}
	if regexp.MustCompile(`(?i)chown\s+root\s+/`).MatchString(lc) {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: chown root / pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "chown_root",
				"matched_pattern": "chown root /",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command attempts 'chown root' on a root path, blocked for safety")
	}

	// 6) FIREWALL DISABLE/FLUSH PATTERNS (THESIS TASK 48 FIX)
	// Block iptables firewall flushes that disable network defenses
	if regexp.MustCompile(`(?i)iptables\s+(-F|--flush)\b`).MatchString(lc) {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: iptables flush pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "iptables_flush",
				"matched_pattern": "iptables -F/--flush",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command flushes iptables firewall rules, blocked for safety")
	}

	// Block disabling UFW firewall
	if regexp.MustCompile(`(?i)ufw\s+disable`).MatchString(lc) {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: ufw disable pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "firewall_disable",
				"matched_pattern": "ufw disable",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command disables UFW firewall, blocked for safety")
	}

	// Block firewalld panic mode disable
	if regexp.MustCompile(`(?i)firewall-cmd\s+--panic-off`).MatchString(lc) {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: firewalld panic-off pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "firewall_disable",
				"matched_pattern": "firewall-cmd --panic-off",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command disables firewalld panic mode, blocked for safety")
	}

	return nil
}

// basicPathSafetyChecks performs very light sanity checks on paths.
//
// NOTE: The DirectorySandbox is still the primary guard for blocking
// actual filesystem traversal outside the allowed root.
// Here we just catch obviously sketchy patterns early.
func basicPathSafetyChecks(cmd string) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	lc := strings.ToLower(cmd)

	// 1) Very broad "delete everything" patterns (extra redundancy)
	//    Examples: rm -rf /   |  rm -rf /*   |  rm -rf ~
	//    (utils.ValidateCommand should already catch the worst, this is a belt+braces check)
	if regexp.MustCompile(`rm\s+-rf\s+(/\s*$|/\*\s*$|~\s*$)`).MatchString(lc) {

		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Broad delete pattern matched
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"static_analyzer",
			"pattern_matched",
			true,
			map[string]interface{}{
				"pattern_type":    "broad_delete",
				"matched_pattern": "rm -rf / or rm -rf /*",
				"blocked":         true,
			},
		)
		return fmt.Errorf("command looks like a mass delete (rm -rf) on a broad target, blocked for safety")
	}

	// 2) Suspicious use of parent traversal in write operations (>, >>, mv, cp)
	writeOps := []string{">>", " > ", "mv ", "cp "}
	if strings.Contains(lc, "..") {
		for _, op := range writeOps {
			if strings.Contains(lc, op) {

				// ─────────────────────────────────────────────────────────────
				// TELEMETRY: Path traversal with write operation matched
				// ─────────────────────────────────────────────────────────────
				tc.Record(
					taskID,
					"safety",
					"static_analyzer",
					"pattern_matched",
					true,
					map[string]interface{}{
						"pattern_type":    "path_traversal_write",
						"matched_pattern": ".. with " + op,
						"blocked":         true,
					},
				)
				return fmt.Errorf("command writes with parent-directory traversal ('..'), blocked for safety")
			}
		}
	}

	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HELPER FUNCTIONS
// ─────────────────────────────────────────────────────────────────────────────

// classifyDangerousPattern categorizes the dangerous pattern for telemetry.
// This provides a standardized pattern type for analysis.
func classifyDangerousPattern(errorMsg string) string {
	switch {
	case strings.Contains(errorMsg, "pipe into a shell"):
		return "pipe_to_shell"
	case strings.Contains(errorMsg, "download a script"):
		return "curl_wget_pipe_shell"
	case strings.Contains(errorMsg, "eval"):
		return "eval_dangerous"
	case strings.Contains(errorMsg, "xargs"):
		return "xargs_shell"
	case strings.Contains(errorMsg, "format a filesystem"):
		return "mkfs_format"
	case strings.Contains(errorMsg, "chmod 777"):
		return "chmod_777"
	case strings.Contains(errorMsg, "chown root"):
		return "chown_root"
	case strings.Contains(errorMsg, "flushes iptables"):
		return "iptables_flush"
	case strings.Contains(errorMsg, "disables UFW"):
		return "firewall_disable_ufw"
	case strings.Contains(errorMsg, "disables firewalld"):
		return "firewall_disable_firewalld"
	case strings.Contains(errorMsg, "mass delete"):
		return "broad_delete"
	case strings.Contains(errorMsg, "parent-directory traversal"):
		return "path_traversal_write"
	default:
		return "unknown_dangerous_pattern"
	}
}

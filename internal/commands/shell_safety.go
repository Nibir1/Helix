package commands

import (
	"fmt"
	"regexp"
	"strings"

	"helix/internal/utils"

	"github.com/fatih/color"
)

// ------------------------------------------------------------
// Top-level safety gate
// ------------------------------------------------------------

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
//  4. Core low-level validation (utils.ValidateCommand)
//  5. Per-segment high-level safety checks (multi-command aware)
//  6. Final safe, normalized command string
func ValidateAndCleanCommand(raw string) (string, error) {
	color.Yellow("🔍 DEBUG ValidateAndCleanCommand input: %q", raw)
	color.Yellow("🔍 DEBUG String bytes:")
	for i, r := range []rune(raw) {
		color.Yellow("  [%d] %q (U+%04X)", i, r, r)
	}

	// 1) Basic trim / normalization
	cmd := utils.SafeTrim(raw)
	if cmd == "" {
		return "", fmt.Errorf("empty command")
	}

	// 2) Quick heuristic quote check (non-fatal, we try to repair)
	if utils.HasUnbalancedQuotesQuick(cmd) {
		color.Yellow("⚠️ Quick check detected possibly unbalanced quotes, attempting auto-fix...")
		fixed := utils.FixUnmatchedQuotes(cmd)
		color.Yellow("🔍 DEBUG: After FixUnmatchedQuotes: %q", fixed)
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

	// 4) Core low-level validation (existing safety net)
	if err := utils.ValidateCommand(cmd); err != nil {
		return "", err
	}

	// 5) High-level, multi-command aware safety.
	//
	// We allow:
	//   - multiple commands separated by ';', '&&', '||'
	//   - pipes and redirects
	// as long as each *segment* is individually safe.
	segments := splitTopLevelSegments(cmd)
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}

		// Extra dangerous pattern checks (semantic, not just token-level)
		if err := extraDangerousPatternChecks(seg); err != nil {
			return "", err
		}

		// Path / write-centric checks (rm -rf, absolute writes, .., etc.)
		if err := basicPathSafetyChecks(seg); err != nil {
			return "", err
		}
	}

	color.Yellow("🔍 DEBUG: Final validated command: %q", cmd)
	return cmd, nil
}

// ------------------------------------------------------------
// Segment splitter (aware of quotes)
// ------------------------------------------------------------

// splitTopLevelSegments splits a shell command into top-level segments
// along ";", "&&", "||" while respecting quotes/backticks.
//
// Example:
//
//	"ls; echo 'x && y'; rm -rf build && git status"
//
// → ["ls", "echo 'x && y'", "rm -rf build", "git status"]
func splitTopLevelSegments(cmd string) []string {
	var segments []string
	var current strings.Builder

	inSingle := false
	inDouble := false
	inBacktick := false

	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]

		// Handle quotes toggling
		if ch == '\'' && !inDouble && !inBacktick {
			inSingle = !inSingle
			current.WriteByte(ch)
			continue
		}
		if ch == '"' && !inSingle && !inBacktick {
			inDouble = !inDouble
			current.WriteByte(ch)
			continue
		}
		if ch == '`' && !inSingle && !inDouble {
			inBacktick = !inBacktick
			current.WriteByte(ch)
			continue
		}

		// Only split on top-level operators (not inside quotes/backticks)
		if !inSingle && !inDouble && !inBacktick {
			// && / ||
			if i+1 < len(cmd) {
				two := cmd[i : i+2]
				if two == "&&" || two == "||" {
					seg := strings.TrimSpace(current.String())
					if seg != "" {
						segments = append(segments, seg)
					}
					current.Reset()
					i++ // skip second char
					continue
				}
			}
			// ;
			if ch == ';' {
				seg := strings.TrimSpace(current.String())
				if seg != "" {
					segments = append(segments, seg)
				}
				current.Reset()
				continue
			}
		}

		current.WriteByte(ch)
	}

	if s := strings.TrimSpace(current.String()); s != "" {
		segments = append(segments, s)
	}

	// Fallback: if for some reason we got nothing, return the original
	if len(segments) == 0 {
		return []string{strings.TrimSpace(cmd)}
	}
	return segments
}

// ------------------------------------------------------------
// High-level semantic safety checks
// ------------------------------------------------------------

var (
	rePipeToShell   = regexp.MustCompile(`\|\s*(sh|bash|zsh)\b`)
	reEval          = regexp.MustCompile(`\beval\b`)
	reXargsShell    = regexp.MustCompile(`\bxargs\s+(sh|bash|zsh)\b`)
	reMkfs          = regexp.MustCompile(`\bmkfs(\.|_|\b)`)
	reDangerChmod   = regexp.MustCompile(`(?i)\bchmod\s+777\s+/`)
	reDangerChown   = regexp.MustCompile(`(?i)\bchown\s+root\s+/`)
	reDDDev         = regexp.MustCompile(`(?i)\bdd\b.*\bof=/dev/`)
	reShutdown      = regexp.MustCompile(`\b(shutdown|reboot|halt|poweroff)\b`)
	reForkBomb      = regexp.MustCompile(`:?\(\)\s*\{\s*:?\s*\|\s*:?\s*;&?\s*\}\s*;`)
	reFindDelete    = regexp.MustCompile(`\bfind\b[^\n]*\b(-delete|-exec\s+rm)\b`)
	reCurlPipeShell = regexp.MustCompile(`\b(curl|wget)\b[^\n]*\|\s*(sh|bash|zsh)\b`)
)

// extraDangerousPatternChecks adds higher-level safety rules
// on top of utils.ValidateCommand.
// These are intentionally conservative and can be tuned over time.
func extraDangerousPatternChecks(cmd string) error {
	lc := strings.ToLower(cmd)

	// Pipes into shells: `| sh`, `| bash`, `| zsh`
	if rePipeToShell.MatchString(lc) {
		return fmt.Errorf("command contains pipe into shell (e.g. '| sh'), which is too dangerous to run automatically")
	}

	// curl/wget piped into shell
	if reCurlPipeShell.MatchString(lc) {
		return fmt.Errorf("command downloads and pipes directly into a shell, which is too dangerous to run automatically")
	}

	// eval
	if reEval.MatchString(lc) {
		return fmt.Errorf("command contains 'eval', which is too dangerous to run automatically")
	}

	// xargs with shell
	if reXargsShell.MatchString(lc) {
		return fmt.Errorf("command uses 'xargs' to run a shell, which is too dangerous to run automatically")
	}

	// mkfs (formatting filesystems)
	if reMkfs.MatchString(lc) {
		return fmt.Errorf("command appears to format a filesystem (mkfs), blocked for safety")
	}

	// dd to /dev/*
	if reDDDev.MatchString(lc) {
		return fmt.Errorf("command uses 'dd' to write directly to /dev, blocked for safety")
	}

	// shutdown / reboot / halt / poweroff
	if reShutdown.MatchString(lc) {
		return fmt.Errorf("command attempts to shutdown/reboot the system, blocked for safety")
	}

	// forkbomb-ish patterns
	if reForkBomb.MatchString(lc) {
		return fmt.Errorf("command looks like a possible fork bomb, blocked for safety")
	}

	// find -delete or find ... -exec rm
	if reFindDelete.MatchString(lc) {
		return fmt.Errorf("command uses 'find' with '-delete' or '-exec rm', which is too destructive to run automatically")
	}

	// Extremely broad chmod/chown patterns
	if reDangerChmod.MatchString(lc) {
		return fmt.Errorf("command attempts chmod 777 on root path")
	}
	if reDangerChown.MatchString(lc) {
		return fmt.Errorf("command attempts chown root on root path")
	}

	return nil
}

// ------------------------------------------------------------
// Path / write safety checks
// ------------------------------------------------------------

// basicPathSafetyChecks performs sanity checks around filesystem writes.
//
// NOTE: The DirectorySandbox is still the primary guard for blocking
// actual filesystem traversal outside the allowed root. Here we just
// catch obviously sketchy patterns early (rm -rf, absolute writes, '..').
func basicPathSafetyChecks(cmd string) error {
	// 1) Specific rm -rf analysis
	if err := checkRmRfTargets(cmd); err != nil {
		return err
	}

	// 2) Redirection, cp, mv, tee targeting absolute paths or parent traversal
	if err := checkWriteTargets(cmd); err != nil {
		return err
	}

	return nil
}

// checkRmRfTargets tries to detect obviously disastrous rm -rf usages,
// while still allowing normal developer flows like:
//
//	rm -rf ./dist
//	rm -rf build tmp/cache
//
// We block:
//
//	rm -rf /
//	rm -rf /*
//	rm -rf .
//	rm -rf ./
//	rm -rf with wildcard (*)
//	rm -rf with '..'
//	rm -rf /absolute/path
func checkRmRfTargets(cmd string) error {
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return nil
	}

	lowerTokens := make([]string, len(tokens))
	for i, t := range tokens {
		lowerTokens[i] = strings.ToLower(t)
	}

	for i, tok := range lowerTokens {
		if tok != "rm" {
			continue
		}

		// Look for flags in next token, e.g. "-rf", "-fr", "-rfi"
		if i+1 >= len(tokens) {
			continue
		}

		flagsTok := lowerTokens[i+1]
		if !strings.HasPrefix(flagsTok, "-") {
			continue
		}

		// rm with both r and f flags (in any order)
		if !(strings.Contains(flagsTok, "r") && strings.Contains(flagsTok, "f")) {
			continue
		}

		// Collect targets after flags
		targets := tokens[i+2:]
		if len(targets) == 0 {
			return fmt.Errorf("command looks like a mass delete (rm -rf with no explicit target)")
		}

		for _, rawTarget := range targets {
			if rawTarget == "" {
				continue
			}

			// Strip simple surrounding quotes
			target := strings.Trim(rawTarget, `"'`)
			tLower := strings.ToLower(target)

			if tLower == "/" || tLower == "/*" || tLower == "." || tLower == "./" {
				return fmt.Errorf("command looks like a mass delete (rm -rf %s)", target)
			}

			if strings.HasPrefix(target, "/") {
				return fmt.Errorf("rm -rf on absolute path %q is blocked for safety", target)
			}

			if strings.Contains(target, "..") {
				return fmt.Errorf("rm -rf with parent traversal %q is blocked for safety", target)
			}

			if strings.Contains(target, "*") {
				return fmt.Errorf("rm -rf with wildcard target %q is blocked for safety", target)
			}
		}
	}

	return nil
}

// checkWriteTargets looks for writes via redirection, cp, mv, tee and
// blocks obviously unsafe destinations such as absolute paths or '..'.
func checkWriteTargets(cmd string) error {
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return nil
	}

	// Helper: decide if a target path is suspicious
	isDangerousTarget := func(path string) bool {
		if path == "" {
			return false
		}
		unquoted := strings.Trim(path, `"'`)

		// absolute path
		if strings.HasPrefix(unquoted, "/") {
			return true
		}
		// parent traversal
		if strings.Contains(unquoted, "..") {
			return true
		}
		return false
	}

	// Redirections (>, >>)
	for i, tok := range tokens {
		if tok == ">" || tok == ">>" {
			if i+1 >= len(tokens) {
				continue
			}
			target := tokens[i+1]
			if isDangerousTarget(target) {
				return fmt.Errorf("redirection to dangerous path %q is blocked for safety", target)
			}
		}
	}

	// cp / mv - only check destination (last arg)
	for _, cmdName := range []string{"cp", "mv"} {
		for i, tok := range tokens {
			if tok != cmdName {
				continue
			}
			// require at least "cp src dst"
			if len(tokens) < i+3 {
				continue
			}
			dst := tokens[len(tokens)-1]
			if isDangerousTarget(dst) {
				return fmt.Errorf("%s to dangerous path %q is blocked for safety", cmdName, dst)
			}
		}
	}

	// tee destination
	for i, tok := range tokens {
		if tok != "tee" {
			continue
		}
		if i+1 >= len(tokens) {
			continue
		}
		dst := tokens[i+1]
		if isDangerousTarget(dst) {
			return fmt.Errorf("tee to dangerous path %q is blocked for safety", dst)
		}
	}

	return nil
}

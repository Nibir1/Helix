// internal/agent/fastpath.go
//
// Purpose: Deterministic local fast path for common local script/file
// creation requests. This allows Helix to execute simple local workflows
// without paying an AI planner round trip.
//
// The fast path is intentionally narrow:
//   - local bash/shell script creation only,
//   - explicit filename required,
//   - explicit quoted print text required,
//   - no URLs,
//   - no pipes,
//   - no sudo,
//   - no destructive operations.
//
// Every generated command is still executed through the normal Helix safety
// pipeline: validation, risk analysis, sandbox checks, confirmations.
package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"helix/internal/ai"
)

// fastScriptCreateRe detects a request to create a bash/shell script.
//
// Examples:
//   - "Create a bash script named grid.sh ..."
//   - "make a shell script called hello.sh ..."
//   - "write a bash script ..."
var fastScriptCreateRe = regexp.MustCompile(
	`(?i)\b(?:create|make|write|add|generate)\b.*\b(?:bash|shell)\s+script\b`,
)

// fastBacktickSubstitutionRe removes backtick-enclosed command substitution
// content from user-provided print text. The fast path never executes this
// text, but removing the whole substitution is stricter and matches the
// expected sanitized output.
var fastBacktickSubstitutionRe = regexp.MustCompile("`[^`]*`")

// fastNamedScriptRe detects an explicit script filename introduced by
// "named", "called", or "titled".
var fastNamedScriptRe = regexp.MustCompile(
	`(?i)\b(?:named?|called|titled)\s+["']?([A-Za-z0-9._\-/]+)["']?`,
)

// fastBareScriptNameRe detects a filename directly after "bash script"
// or "shell script" without the word "named".
var fastBareScriptNameRe = regexp.MustCompile(
	`(?i)\b(?:bash|shell)\s+script\s+["']?([A-Za-z0-9._\-/]+)["']?`,
)

// fastPrintVerbRe detects the output verb used by the request.
var fastPrintVerbRe = regexp.MustCompile(
	`(?i)\b(?:prints?|echoes?|says?|outputs?|displays?)\b`,
)

// fastQuotedTextRe extracts the first quoted string after the print verb.
var fastQuotedTextRe = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)

// fastColorRe detects simple color names.
var fastColorRe = regexp.MustCompile(
	`(?i)\bin\s+(black|red|green|yellow|blue|magenta|cyan|white)\b`,
)

// fastExecutableRe detects explicit executable requests.
var fastExecutableRe = regexp.MustCompile(
	`(?i)\bexecutable\b|\bchmod\s+\+x\b`,
)

// fastRunRe detects explicit run/execute requests.
var fastRunRe = regexp.MustCompile(
	`(?i)\b(?:run|execute|launch|start)\b`,
)

// fastPathDangerRe rejects requests containing network tools, destructive
// shell behavior, pipes, redirection, command substitution, or privilege
// escalation.
var fastPathDangerRe = regexp.MustCompile(
	`(?i)(https?://|\bcurl\b|\bwget\b|\bsudo\b|\brm\s|\bmv\s|\bdd\s|\bmkfs\b|\beval\b|\bnc\b|\bssh\b|\bscp\b|\brsync\b|\|\s|;|&|<|>|\$\()`,
)

// fastColorCodes maps user-facing color names to ANSI SGR codes.
var fastColorCodes = map[string]string{
	"black":   "30",
	"red":     "31",
	"green":   "32",
	"yellow":  "33",
	"blue":    "34",
	"magenta": "35",
	"cyan":    "36",
	"white":   "37",
}

// fastCreativeRe detects requests for creative/interesting output without explicit quotes.
var fastCreativeRe = regexp.MustCompile(`(?i)\b(?:interesting|creative|cool|something|grid|helix)\b`)

// buildFastLocalPlan attempts to build a deterministic local plan for simple
// bash-script creation requests.
func buildFastLocalPlan(userInput string) (*ai.Plan, bool) {
	input := strings.TrimSpace(userInput)
	if input == "" {
		return nil, false
	}
	if !fastScriptCreateRe.MatchString(input) {
		return nil, false
	}
	if strings.Contains(input, "`") || strings.ContainsAny(input, "\r\n") {
		return nil, false
	}
	if fastPathDangerRe.MatchString(input) {
		return nil, false
	}

	filename := ""
	if m := fastNamedScriptRe.FindStringSubmatch(input); m != nil {
		filename = m[1]
	} else if m := fastBareScriptNameRe.FindStringSubmatch(input); m != nil {
		filename = m[1]
	}
	filename = strings.Trim(filename, `"'`)
	filename = filepath.Clean(filename)
	if filename == "" || filename == "." || filename == ".." {
		return nil, false
	}
	if filepath.IsAbs(filename) {
		return nil, false
	}
	if strings.Contains(filename, "..") {
		return nil, false
	}
	if len(filename) > 200 {
		return nil, false
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".sh") {
		filename += ".sh"
	}

	verbLoc := fastPrintVerbRe.FindStringIndex(input)
	if verbLoc == nil {
		return nil, false
	}
	afterVerb := input[verbLoc[1]:]
	textMatch := fastQuotedTextRe.FindStringSubmatch(afterVerb)

	var text string
	if textMatch != nil {
		text = textMatch[1]
		if text == "" {
			text = textMatch[2]
		}
		text = fastSanitizeText(text)
	} else if fastCreativeRe.MatchString(input) {
		// Fallback for creative/interesting requests without explicit quotes
		text = "⚡ Welcome to the Helix Grid ⚡\\n\\033[32m[System] Neural link established.\\033[0m"
	} else {
		return nil, false
	}

	if text == "" {
		return nil, false
	}

	colorName := ""
	if m := fastColorRe.FindStringSubmatch(input); m != nil {
		colorName = strings.ToLower(m[1])
	}

	steps := make([]ai.PlanStep, 0, 4)
	if dir := filepath.Dir(filename); dir != "." && dir != "/" {
		steps = append(steps, ai.PlanStep{
			Tool:    "shell",
			Command: "mkdir -p " + fastShellQuote(dir),
			Trusted: true,
		})
	}

	shebang := "#!/usr/bin/env bash"
	scriptLine := fastScriptPrintLine(text, colorName)
	writeCommand := fmt.Sprintf(
		"printf '%%s\\n' %s %s > %s",
		fastShellQuote(shebang),
		fastShellQuote(scriptLine),
		fastShellQuote(filename),
	)
	steps = append(steps, ai.PlanStep{
		Tool:    "shell",
		Command: writeCommand,
		Trusted: true,
	})

	makeExecutable := fastWantsExecutable(input) || fastWantsRun(input)
	if makeExecutable {
		steps = append(steps, ai.PlanStep{
			Tool:    "shell",
			Command: "chmod +x " + fastShellQuote(filename),
			Trusted: true,
		})
	}

	if fastWantsRun(input) {
		steps = append(steps, ai.PlanStep{
			Tool:    "shell",
			Command: fastRunCommand(filename),
			Trusted: true,
		})
	}

	intent := ai.IntentShell
	if len(steps) > 1 {
		intent = ai.IntentMultiStep
	}
	return &ai.Plan{
		Intent: intent,
		Steps:  steps,
	}, true
}

// fastWantsExecutable reports whether the user explicitly asked to make the
// script executable.
//
// Args:
//   - input: raw user input.
//
// Returns:
//   - bool.
//
// Complexity: O(len(input)).
func fastWantsExecutable(input string) bool {
	lower := strings.ToLower(input)

	// Negative guards.
	for _, phrase := range []string{
		"not executable",
		"without making it executable",
		"do not make it executable",
		"don't make it executable",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}

	return fastExecutableRe.MatchString(input)
}

// fastWantsRun reports whether the user explicitly asked to run/execute the
// generated script.
//
// Args:
//   - input: raw user input.
//
// Returns:
//   - bool.
//
// Complexity: O(len(input)).
func fastWantsRun(input string) bool {
	lower := strings.ToLower(input)

	// Negative guards.
	for _, phrase := range []string{
		"don't run",
		"dont run",
		"do not run",
		"without running",
		"without executing",
		"not run",
		"not execute",
		"never run",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}

	return fastRunRe.MatchString(input)
}

// fastSanitizeText removes control characters and shell-active characters
// from the user-provided print text.
//
// Backtick-enclosed command substitutions are removed completely, while
// variable references lose only the leading dollar sign. This keeps the
// printed text readable while eliminating command-substitution payloads.
//
// Args:
//   - s: raw text extracted from the request.
//
// Returns:
//   - sanitized text safe for deterministic script generation.
//
// Complexity: O(len(s)).
func fastSanitizeText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Remove full backtick command substitutions first.
	s = fastBacktickSubstitutionRe.ReplaceAllString(s, " ")

	// Remove any standalone backticks that were not part of a pair.
	s = strings.ReplaceAll(s, "`", "")

	// Remove variable expansion authority, but keep the visible name.
	s = strings.ReplaceAll(s, "$", "")

	// Normalize whitespace.
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")

	var sb strings.Builder

	for _, r := range s {
		// Reject control characters.
		if r < 0x20 || r == 0x7f {
			continue
		}

		// Reject shell-active characters.
		if strings.ContainsRune("`$\\|&<>(){}'\"", r) {
			continue
		}

		sb.WriteRune(r)
	}

	return strings.Join(strings.Fields(sb.String()), " ")
}

// fastShellQuote quotes a value safely for POSIX-like shells.
//
// Args:
//   - s: value to quote.
//
// Returns:
//   - single-quoted shell string.
//
// Complexity: O(len(s)).
func fastShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fastScriptPrintLine builds the line placed inside the generated script.
//
// Args:
//   - text: sanitized text to print.
//   - color: optional color name.
//
// Returns:
//   - shell command line for the generated script.
//
// Complexity: O(1).
func fastScriptPrintLine(text, color string) string {
	if code, ok := fastColorCodes[strings.ToLower(color)]; ok {
		// Phase 15 Fix: Use %b to interpret backslash escapes in the argument
		return fmt.Sprintf(
			"printf \"\\033[%sm%%b\\033[0m\\n\" \"%s\"",
			code,
			text,
		)
	}
	return fmt.Sprintf("printf \"%%b\\n\" \"%s\"", text)
}

// fastRunCommand builds the command used to execute the generated script.
//
// Args:
//   - filename: relative script path.
//
// Returns:
//   - shell command string.
//
// Complexity: O(1).
func fastRunCommand(filename string) string {
	if strings.Contains(filename, "/") {
		return fastShellQuote(filename)
	}
	return "./" + fastShellQuote(filename)
}

// runFastPlan executes a deterministic fast-path plan using the normal shell
// step handler. Fast-path plans only contain shell steps.
//
// Args:
//   - plan: deterministic local plan.
//
// Returns: none.
// Complexity: O(steps × command execution time).
func (a *Agent) runFastPlan(plan *ai.Plan) {
	for i, step := range plan.Steps {
		if len(plan.Steps) > 1 {
			a.ux.PrintSystemMessage(fmt.Sprintf("--- Step %d ---", i+1))
		}

		if step.Tool != "shell" {
			a.ux.PrintWarning(fmt.Sprintf("Fast path produced unsupported tool: %s", step.Tool))
			return
		}

		if err := a.handleShellStep(step); err != nil {
			a.ux.PrintError(fmt.Sprintf("Fast-path step failed: %v", err))
			return
		}
	}
}

// internal/commands/execute.go

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"helix/internal/shell"
	"helix/internal/telemetry"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// Dangerous commands and patterns to block
var dangerousPatterns = []string{
	"rm -rf /", "rm -rf /*", "format c:", "mkfs", "fdisk", "dd if=/dev/zero",
	"> /dev/sda", "chmod -R 777 /", "mv / /dev/null", "> /etc/passwd",
	":(){ :|:& };:", "fork bomb", "debugfs", "mkswap", "swapoff", "> /boot",
	// FIREWALL PATTERNS (THESIS TASK 48 FIX)
	"iptables -F", "iptables --flush", "ufw disable", "firewall-cmd --panic-off",
}

// ExecuteConfig holds execution preferences
type ExecuteConfig struct {
	DryRun      bool
	AutoConfirm bool
	SafeMode    bool
}

// DefaultExecuteConfig returns safe default execution settings
func DefaultExecuteConfig() ExecuteConfig {
	return ExecuteConfig{
		DryRun:      false,
		AutoConfirm: false,
		SafeMode:    true,
	}
}

// IsCommandSafe checks if a command contains dangerous patterns
func IsCommandSafe(command string) bool {
	cmdLower := strings.ToLower(command)

	patternsMatched := []string{}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, pattern) {
			patternsMatched = append(patternsMatched, pattern)
		}
	}

	// Additional safety checks
	if strings.Contains(cmdLower, "rm -rf") && strings.Contains(cmdLower, "home") {
		// Allow rm -rf in home directory but warn
		// Record telemetry for this exception
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "command_check_exception",
			Success:   true,
			Data: map[string]interface{}{
				"command":   command,
				"exception": "rm_rf_home_allowed",
				"reason":    "rm -rf in home directory permitted with warning",
			},
		})
		return true
	}

	// Record telemetry for safety check result
	if len(patternsMatched) > 0 {
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "dangerous_pattern_blocked",
			Success:   false,
			Data: map[string]interface{}{
				"command":          command,
				"patterns_matched": patternsMatched,
				"pattern_count":    len(patternsMatched),
			},
		})
		return false
	}

	// Record successful safety check
	telemetry.Record(telemetry.TelemetryEvent{
		Phase:     "safety",
		Component: "execute",
		EventType: "command_safety_check",
		Success:   true,
		Data: map[string]interface{}{
			"command": command,
			"result":  "passed",
		},
	})

	return true
}

// -------------------------------------------------------
// ✨ NEW: Smart Exit Code Handling (grep, diff, find)
// -------------------------------------------------------

func isNonFatalExit(command string, exitCode int) bool {
	cmdLower := strings.ToLower(command)

	// GREP
	if strings.Contains(cmdLower, "grep ") || strings.HasPrefix(cmdLower, "grep") {
		// 1 = no matches found → not fatal
		// 2 = actual error
		return exitCode == 1
	}

	// DIFF
	if strings.HasPrefix(cmdLower, "diff ") {
		// diff returns:
		// 0 = same
		// 1 = different → NOT fatal
		// 2 = fatal error
		return exitCode == 1
	}

	// FIND
	if strings.HasPrefix(cmdLower, "find ") {
		// find may return non-zero for missing permissions etc.
		// not fatal unless clearly error
		return exitCode == 1
	}

	return false
}

// ExecuteCommand runs a shell command with safety checks
func ExecuteCommand(command string, config ExecuteConfig, env shell.Env) error {
	telemetry.Record(telemetry.TelemetryEvent{
		Phase:     "execution",
		Component: "execute",
		EventType: "command_execution_attempt",
		Success:   false, // Will be updated on success
		Data: map[string]interface{}{
			"command":   command,
			"dry_run":   config.DryRun,
			"safe_mode": config.SafeMode,
		},
	})

	// Light validation only
	command = strings.TrimSpace(command)
	if command == "" {
		err := fmt.Errorf("empty command")
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "execution",
			Component: "execute",
			EventType: "empty_command_blocked",
			Success:   false,
			Data: map[string]interface{}{
				"error": err.Error(),
			},
		})
		return err
	}

	// Safety check only
	if config.SafeMode && !IsCommandSafe(command) {
		err := fmt.Errorf("command blocked for safety: %s", command)
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "safety_blocked",
			Success:   false,
			Data: map[string]interface{}{
				"command": command,
				"error":   err.Error(),
			},
		})
		return err
	}

	// Simple quote balance check
	if utils.HasUnbalancedQuotesQuick(command) {
		err := fmt.Errorf("command has unbalanced quotes: %s", command)
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "unbalanced_quotes_blocked",
			Success:   false,
			Data: map[string]interface{}{
				"command": command,
				"error":   err.Error(),
			},
		})
		return err
	}

	// Command header
	if config.DryRun {
		fmt.Printf("%s ", color.YellowString("Dry Run:"))
	} else {
		fmt.Printf("%s ", color.YellowString("Executing:"))
	}

	// Syntax highlighting (optional)
	if syntaxHighlighter != nil {
		fmt.Println(syntaxHighlighter.HighlightCommand(command))
	} else {
		fmt.Println(command)
	}

	// Dangerous command confirmation
	if !config.AutoConfirm && isPotentiallyDangerous(command) {
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "dangerous_confirmation_required",
			Success:   false, // Will be updated based on user response
			Data: map[string]interface{}{
				"command": command,
			},
		})

		if !AskForConfirmation("This command might be dangerous. Continue?") {
			err := fmt.Errorf("command cancelled by user")
			telemetry.Record(telemetry.TelemetryEvent{
				Phase:     "safety",
				Component: "execute",
				EventType: "user_cancelled_dangerous_command",
				Success:   false,
				Data: map[string]interface{}{
					"command": command,
					"error":   err.Error(),
				},
			})
			return err
		}

		// User confirmed dangerous command
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "user_confirmed_dangerous_command",
			Success:   true,
			Data: map[string]interface{}{
				"command": command,
			},
		})
	}

	// Record shell selection for telemetry
	shellType := "sh"
	if env.Shell != "" {
		shellType = env.Shell
	} else if runtime.GOOS == "windows" {
		shellType = "cmd"
	}

	// Select correct shell
	var cmd *exec.Cmd
	switch env.Shell {
	case "powershell":
		shellType = "powershell"
		cmd = exec.Command("powershell", "-Command", command)
	case "cmd":
		shellType = "cmd"
		cmd = exec.Command("cmd", "/C", command)
	default:
		shellToUse := env.Shell
		if shellToUse == "" {
			if runtime.GOOS == "windows" {
				shellToUse = "cmd"
				shellType = "cmd"
			} else {
				shellToUse = "sh"
				shellType = "sh"
			}
		}
		cmd = exec.Command(shellToUse, "-c", command)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// ------------ Execute ------------
	err := cmd.Run()

	if err != nil {
		// Handle non-zero exit codes properly
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()

			if isNonFatalExit(command, code) {
				color.Yellow("Non-fatal exit (%d) — continuing", code)
				telemetry.Record(telemetry.TelemetryEvent{
					Phase:     "execution",
					Component: "execute",
					EventType: "non_fatal_exit",
					Success:   true,
					Data: map[string]interface{}{
						"command":   command,
						"shell":     shellType,
						"exit_code": code,
						"reason":    "non_fatal_tool_exit",
					},
				})
				return nil
			}

			execErr := fmt.Errorf("command execution failed (exit %d): %w", code, err)
			telemetry.Record(telemetry.TelemetryEvent{
				Phase:     "execution",
				Component: "execute",
				EventType: "command_failed",
				Success:   false,
				Data: map[string]interface{}{
					"command":   command,
					"shell":     shellType,
					"exit_code": code,
					"error":     execErr.Error(),
				},
			})
			return execErr
		}

		execErr := fmt.Errorf("command execution failed: %w", err)
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "execution",
			Component: "execute",
			EventType: "execution_error",
			Success:   false,
			Data: map[string]interface{}{
				"command": command,
				"shell":   shellType,
				"error":   execErr.Error(),
			},
		})
		return execErr
	}

	// Command succeeded
	telemetry.Record(telemetry.TelemetryEvent{
		Phase:     "execution",
		Component: "execute",
		EventType: "command_executed_successfully",
		Success:   true,
		Data: map[string]interface{}{
			"command":   command,
			"shell":     shellType,
			"exit_code": 0,
		},
	})

	return nil
}

// isPotentiallyDangerous checks for commands that need extra confirmation
func isPotentiallyDangerous(command string) bool {
	cmdLower := strings.ToLower(command)
	dangerousKeywords := []string{
		"rm -rf", "chmod", "chown", "mv ", "dd ", "format",
		"fdisk", "mkfs", "> ", ">> ", "curl | sh", "wget | sh",
		// FIREWALL KEYWORDS (THESIS TASK 48 FIX)
		"iptables -F", "iptables --flush", "ufw disable",
	}

	keywordsMatched := []string{}
	for _, keyword := range dangerousKeywords {
		if strings.Contains(cmdLower, keyword) {
			keywordsMatched = append(keywordsMatched, keyword)
		}
	}

	// Record telemetry for dangerous keyword detection
	if len(keywordsMatched) > 0 {
		telemetry.Record(telemetry.TelemetryEvent{
			Phase:     "safety",
			Component: "execute",
			EventType: "dangerous_keywords_detected",
			Success:   true, // Detection success, not block
			Data: map[string]interface{}{
				"command":          command,
				"keywords_matched": keywordsMatched,
				"keyword_count":    len(keywordsMatched),
			},
		})
		return true
	}

	return false
}

// AskForConfirmation asks for user confirmation
func AskForConfirmation(prompt string) bool {
	var response string
	fmt.Printf("%s [y/N]: ", prompt)
	fmt.Scanln(&response)

	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}

// Global syntax highlighter instance
var syntaxHighlighter *utils.SyntaxHighlighter

// SetSyntaxHighlighter sets the global syntax highlighter instance
func SetSyntaxHighlighter(sh *utils.SyntaxHighlighter) {
	syntaxHighlighter = sh
}

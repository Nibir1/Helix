// internal/commands/execute.go

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"helix/internal/shell"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// Dangerous commands and patterns to block
var dangerousPatterns = []string{
	"rm -rf /", "rm -rf /*", "format c:", "mkfs", "fdisk", "dd if=/dev/zero",
	"> /dev/sda", "chmod -R 777 /", "mv / /dev/null", "> /etc/passwd",
	":(){ :|:& };:", "fork bomb", "debugfs", "mkswap", "swapoff", "> /boot",
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

	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, pattern) {
			return false
		}
	}

	// Additional safety checks
	if strings.Contains(cmdLower, "rm -rf") && strings.Contains(cmdLower, "home") {
		// Allow rm -rf in home directory but warn
		return true
	}

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
	// Light validation only
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("empty command")
	}

	// Safety check only
	if config.SafeMode && !IsCommandSafe(command) {
		return fmt.Errorf("command blocked for safety: %s", command)
	}

	// Simple quote balance check
	if utils.HasUnbalancedQuotesQuick(command) {
		return fmt.Errorf("command has unbalanced quotes: %s", command)
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
		if !AskForConfirmation("This command might be dangerous. Continue?") {
			return fmt.Errorf("command cancelled by user")
		}
	}

	// Select correct shell
	var cmd *exec.Cmd
	switch env.Shell {
	case "powershell":
		cmd = exec.Command("powershell", "-Command", command)
	case "cmd":
		cmd = exec.Command("cmd", "/C", command)
	default:
		shellToUse := env.Shell
		if shellToUse == "" {
			if runtime.GOOS == "windows" {
				shellToUse = "cmd"
			} else {
				shellToUse = "sh"
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
				return nil
			}

			return fmt.Errorf("command execution failed (exit %d): %w", code, err)
		}

		return fmt.Errorf("command execution failed: %w", err)
	}

	return nil
}

// isPotentiallyDangerous checks for commands that need extra confirmation
func isPotentiallyDangerous(command string) bool {
	cmdLower := strings.ToLower(command)
	dangerousKeywords := []string{
		"rm -rf", "chmod", "chown", "mv ", "dd ", "format",
		"fdisk", "mkfs", "> ", ">> ", "curl | sh", "wget | sh",
	}

	for _, keyword := range dangerousKeywords {
		if strings.Contains(cmdLower, keyword) {
			return true
		}
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

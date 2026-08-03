// internal/commands/execute.go
// Purpose: Low-level command execution with safety checks.
// Phase 0 change: confirmation prompts are routed through the Prompter abstraction.
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

// dangerousPatterns are hard-blocked command fragments.
var dangerousPatterns = []string{
	"rm -rf /", "rm -rf /*", "format c:", "mkfs", "fdisk", "dd if=/dev/zero",
	"> /dev/sda", "chmod -R 777 /", "mv / /dev/null", "> /etc/passwd",
	":(){ :|:& };:", "fork bomb", "debugfs", "mkswap", "swapoff", "> /boot",
}

// ExecuteConfig holds execution preferences.
type ExecuteConfig struct {
	DryRun      bool
	AutoConfirm bool
	SafeMode    bool
}

// DefaultExecuteConfig returns safe default execution settings.
func DefaultExecuteConfig() ExecuteConfig {
	return ExecuteConfig{
		DryRun:      false,
		AutoConfirm: false,
		SafeMode:    true,
	}
}

// IsCommandSafe checks for known dangerous patterns.
func IsCommandSafe(command string) bool {
	cmdLower := strings.ToLower(command)

	for _, pattern := range dangerousPatterns {
		if strings.Contains(cmdLower, pattern) {
			return false
		}
	}

	return true
}

// isNonFatalExit handles tools whose non-zero exit codes are often informational.
func isNonFatalExit(command string, exitCode int) bool {
	cmdLower := strings.ToLower(command)

	// grep: 1 means no matches, not a fatal error.
	if strings.Contains(cmdLower, "grep ") || strings.HasPrefix(cmdLower, "grep") {
		return exitCode == 1
	}

	// diff: 1 means differences found.
	if strings.HasPrefix(cmdLower, "diff ") {
		return exitCode == 1
	}

	// find: 1 can occur for permission issues but may still produce output.
	if strings.HasPrefix(cmdLower, "find ") {
		return exitCode == 1
	}

	return false
}

// ExecuteCommand runs a shell command with safety checks.
func ExecuteCommand(command string, config ExecuteConfig, env shell.Env) error {
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("empty command")
	}

	if config.SafeMode && !IsCommandSafe(command) {
		return fmt.Errorf("command blocked for safety: %s", command)
	}

	if utils.HasUnbalancedQuotesQuick(command) {
		return fmt.Errorf("command has unbalanced quotes: %s", command)
	}

	if config.DryRun {
		fmt.Printf("%s ", color.YellowString("Dry Run:"))
	} else {
		fmt.Printf("%s ", color.YellowString("Executing:"))
	}

	if syntaxHighlighter != nil {
		fmt.Println(syntaxHighlighter.HighlightCommand(command))
	} else {
		fmt.Println(command)
	}

	if !config.AutoConfirm && isPotentiallyDangerous(command) {
		if !AskForConfirmation("This command might be dangerous. Continue?") {
			return fmt.Errorf("command cancelled by user")
		}
	}

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

	err := cmd.Run()
	if err != nil {
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

// isPotentiallyDangerous detects commands requiring explicit confirmation.
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

// syntaxHighlighter is the global command highlighter.
var syntaxHighlighter *utils.SyntaxHighlighter

// SetSyntaxHighlighter sets the global syntax highlighter.
func SetSyntaxHighlighter(sh *utils.SyntaxHighlighter) {
	syntaxHighlighter = sh
}

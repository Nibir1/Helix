package commands

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"helix/internal/ai"
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

// ValidateAndCleanCommand ensures the command is safe and properly formatted
func ValidateAndCleanCommand(command string) (string, error) {
	command = strings.TrimSpace(command)

	// ADD DEBUG
	color.Yellow("🔍 DEBUG ValidateAndCleanCommand input: '%s'", command)

	// DEBUG: Check the actual bytes
	utils.DebugStringBytes(command)

	// Remove any remaining backticks or code block markers
	command = strings.ReplaceAll(command, "`", "")
	command = strings.ReplaceAll(command, "```", "")

	// Remove any markdown formatting
	command = strings.ReplaceAll(command, "**", "")
	command = strings.ReplaceAll(command, "*", "")

	// Remove leading/trailing quotes
	command = strings.Trim(command, `"'`)

	// FIXED: Use utils package
	command = utils.FixUnmatchedQuotes(command)

	// FIXED: Use utils package
	color.Yellow("🔍 DEBUG: Before HasBalancedQuotes check: '%s'", command)
	if !utils.HasBalancedQuotes(command) {
		return "", fmt.Errorf("command has unmatched quotes: %s", command)
	}

	// Check if command is empty after cleaning
	if command == "" {
		return "", fmt.Errorf("empty command after cleaning")
	}

	// Basic command structure validation
	if strings.Contains(command, "\n") {
		// Take only the first line for multi-line commands
		lines := strings.Split(command, "\n")
		command = strings.TrimSpace(lines[0])
	}

	// Safety validation
	if err := utils.ValidateCommand(command); err != nil {
		return "", err
	}

	return command, nil
}

// ExecuteCommand runs a shell command with safety checks
func ExecuteCommand(command string, config ExecuteConfig, env shell.Env) error {
	// Light validation only - command should already be cleaned
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("empty command")
	}

	// Safety check only - don't re-clean the command
	if config.SafeMode && !IsCommandSafe(command) {
		return fmt.Errorf("command blocked for safety: %s", command)
	}

	// Quick quote balance check without aggressive fixing
	singleQuotes := strings.Count(command, "'")
	doubleQuotes := strings.Count(command, `"`)
	if (singleQuotes%2 != 0 && singleQuotes > 1) || (doubleQuotes%2 != 0 && doubleQuotes > 1) {
		return fmt.Errorf("command has unbalanced quotes: %s", command)
	}

	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("empty command")
	}

	// Safety check
	if config.SafeMode && !IsCommandSafe(command) {
		return fmt.Errorf("command blocked for safety: %s", command)
	}

	// NEW: Display the command with syntax highlighting
	if config.DryRun {
		fmt.Printf("%s ", color.YellowString("🚀 Dry Run:"))
	} else {
		fmt.Printf("%s ", color.YellowString("🚀 Executing:"))
	}

	// Use syntax highlighter if available, otherwise fall back
	if syntaxHighlighter != nil {
		highlighted := syntaxHighlighter.HighlightCommand(command)
		fmt.Println(highlighted)
	} else {
		fmt.Println(command)
	}

	// Ask for confirmation for potentially dangerous commands
	if !config.AutoConfirm && isPotentiallyDangerous(command) {
		if !AskForConfirmation("This command might be dangerous. Continue?") {
			return fmt.Errorf("command cancelled by user")
		}
	}

	// Execute based on shell type
	var cmd *exec.Cmd
	switch env.Shell {
	case "powershell":
		cmd = exec.Command("powershell", "-Command", command)
	case "cmd":
		cmd = exec.Command("cmd", "/C", command)
	case "bash", "zsh", "fish":
		cmd = exec.Command(env.Shell, "-c", command)
	default:
		// Fallback to system default
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", command)
		} else {
			cmd = exec.Command("sh", "-c", command)
		}
	}

	// Capture output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Execute
	if err := cmd.Run(); err != nil {
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

// ExplainCommand uses AI to explain what a command does
func ExplainCommand(command string) (string, error) {
	// Note: This function will need to be updated when we fix the prompt builder
	// For now, we'll use a basic implementation
	env := shell.DetectEnvironment()
	promptBuilder := ai.NewPromptBuilder(env, utils.IsOnline(5*time.Second))
	explainPrompt := promptBuilder.BuildExplainPrompt(command)

	// Add debug output
	// color.Yellow("🔍 Debug - Explain prompt: %s", explainPrompt)

	response, err := ai.RunModel(explainPrompt)
	if err != nil {
		return "", fmt.Errorf("AI explanation failed: %w", err)
	}

	// Add debug output for raw response
	color.Yellow("🔍 Debug - Raw AI response: '%s'", response)

	// Clean the response
	cleaned := utils.CleanAIResponse(response)
	color.Yellow("🔍 Debug - Cleaned response: '%s'", cleaned)

	return cleaned, nil
}

// Global syntax highlighter instance (will be set from main)
var syntaxHighlighter *utils.SyntaxHighlighter

// SetSyntaxHighlighter sets the global syntax highlighter instance
func SetSyntaxHighlighter(sh *utils.SyntaxHighlighter) {
	syntaxHighlighter = sh
}

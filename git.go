// git.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
)

// GitManager handles git-specific operations with AI assistance
type GitManager struct {
	env        Env
	execConfig ExecuteConfig
	workingDir string
	sandbox    *DirectorySandbox
}

// NewGitManager creates a new Git manager
func NewGitManager(env Env, execConfig ExecuteConfig, sandbox *DirectorySandbox) *GitManager {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	return &GitManager{
		env:        env,
		execConfig: execConfig,
		workingDir: wd,
		sandbox:    sandbox, // Initialize sandbox
	}
}

// GitOperation represents a git operation with safety checks
type GitOperation struct {
	Description  string
	Command      string
	Confirmation string
	Risks        []string
}

// HandleGitRequest processes natural language git requests
func (gm *GitManager) HandleGitRequest(request string) error {
	request = strings.ToLower(strings.TrimSpace(request))

	color.Blue("🔧 Processing git request: %s", request)

	// First, validate we're in a git repository
	if !gm.isGitRepository() {
		color.Red("❌ Not a git repository")
		color.Yellow("💡 Current directory: %s", gm.workingDir)
		color.Yellow("💡 Navigate to a git repository first or run 'git init'")
		return fmt.Errorf("not a git repository")
	}

	// Check for common complex git operations
	if operation := gm.detectComplexGitOperation(request); operation != nil {
		return gm.executeGitOperation(operation)
	}

	// Fall back to AI for other git requests
	return gm.handleAIGitRequest(request)
}

// isGitRepository checks if current directory is a git repository
func (gm *GitManager) isGitRepository() bool {
	// Check if .git directory exists
	if _, err := os.Stat(filepath.Join(gm.workingDir, ".git")); err == nil {
		return true
	}

	// Alternative: try running git command
	cmd := exec.Command("git", "status")
	cmd.Dir = gm.workingDir
	err := cmd.Run()
	return err == nil
}

// getCurrentBranch gets the current git branch
func (gm *GitManager) getCurrentBranch() (string, error) {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = gm.workingDir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// getAvailableBranches gets list of available branches
func (gm *GitManager) getAvailableBranches() ([]string, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = gm.workingDir
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var branches []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.Contains(line, "HEAD") {
			// Remove the * from current branch and remote prefixes
			line = strings.TrimPrefix(line, "* ")
			line = strings.TrimPrefix(line, "remotes/")
			branches = append(branches, line)
		}
	}
	return branches, nil
}

// detectComplexGitOperation identifies common complex git workflows
func (gm *GitManager) detectComplexGitOperation(request string) *GitOperation {
	// Your specific use case: merge with squash and accept all incoming
	if strings.Contains(request, "merge") && strings.Contains(request, "squash") &&
		(strings.Contains(request, "accept all") || strings.Contains(request, "incoming")) {
		return &GitOperation{
			Description:  "Merge branch with squash and accept all incoming changes",
			Command:      "git merge --squash ${BRANCH}; git checkout --theirs .; git add .; ${COMMIT_CMD}",
			Confirmation: "This will:\n• Squash all commits from the branch into one\n• Accept ALL incoming changes (overwrite local conflicts)\n• Create a new commit with default message",
			Risks: []string{
				"Permanently overwrites local changes in case of conflicts",
				"Loses individual commit history from the merged branch",
				"Uses default commit message - edit if needed",
			},
		}

	}

	// Merge with squash only
	if strings.Contains(request, "merge") && strings.Contains(request, "squash") {
		return &GitOperation{
			Description:  "Merge branch with squash",
			Command:      "git merge --squash ${BRANCH}",
			Confirmation: "This will squash all commits from the branch into staged changes. You'll need to commit manually.",
			Risks: []string{
				"Loses individual commit history from the merged branch",
				"Requires manual commit",
			},
		}
	}

	// Undo last commit but keep changes
	if (strings.Contains(request, "undo") || strings.Contains(request, "remove")) && strings.Contains(request, "commit") {
		return &GitOperation{
			Description:  "Undo last commit but keep changes",
			Command:      "git reset --soft HEAD~1",
			Confirmation: "This will undo the last commit but keep all changes staged.",
			Risks: []string{
				"Removes the last commit from history",
				"Changes remain staged for recommit",
			},
		}
	}

	// Clean untracked files
	if strings.Contains(request, "clean") && strings.Contains(request, "untracked") {
		return &GitOperation{
			Description:  "Clean untracked files and directories",
			Command:      "git clean -fd",
			Confirmation: "This will permanently delete all untracked files and directories.",
			Risks: []string{
				"Permanently deletes untracked files",
				"Cannot be undone",
			},
		}
	}

	// Stash all changes
	if strings.Contains(request, "stash") && (strings.Contains(request, "all") || strings.Contains(request, "everything")) {
		return &GitOperation{
			Description:  "Stash all changes including untracked files",
			Command:      "git stash --include-untracked",
			Confirmation: "This will stash all changes including untracked files.",
			Risks: []string{
				"Temporarily removes all uncommitted changes",
				"Use 'git stash pop' to restore later",
			},
		}
	}

	// Change last commit (amend)
	if (strings.Contains(request, "change") || strings.Contains(request, "amend")) && strings.Contains(request, "commit") {
		return &GitOperation{
			Description:  "Amend the most recent commit",
			Command:      "git commit --amend",
			Confirmation: "This will modify the most recent commit. If already pushed, force push will be needed.",
			Risks: []string{
				"Changes commit history",
				"May require force push if already pushed",
			},
		}
	}

	return nil
}

// executeGitOperation safely executes a git operation with confirmation
func (gm *GitManager) executeGitOperation(operation *GitOperation) error {
	color.Cyan("\n📋 Operation: %s", operation.Description)
	color.Yellow("🚀 Command: %s", operation.Command)

	// Show current directory and branch info
	currentBranch, err := gm.getCurrentBranch()
	if err == nil {
		color.Blue("📍 Current branch: %s", currentBranch)
	}
	color.Blue("📁 Repository: %s", gm.workingDir)

	// Show risks
	if len(operation.Risks) > 0 {
		color.Red("⚠️  Risks:")
		for _, risk := range operation.Risks {
			color.Red("   • %s", risk)
		}
		fmt.Println()
	}

	// Get confirmation
	if !AskForConfirmation(operation.Confirmation) {
		color.Yellow("❌ Operation cancelled")
		return nil
	}

	// Handle complex multi-command operations
	if gm.isMultiCommandOperation(operation) {
		return gm.executeMultiCommandOperation(operation)
	}

	// Handle branch replacement for single commands
	finalCommand := operation.Command
	if strings.Contains(finalCommand, "${BRANCH}") {
		branch, err := gm.getTargetBranch()
		if err != nil {
			return err
		}
		// Use unquoted branch name - let the command execution handle escaping
		finalCommand = strings.ReplaceAll(finalCommand, "${BRANCH}", branch)
		color.Green("🎯 Target branch: %s", branch)
	}

	// Final confirmation for destructive operations
	if gm.isDestructiveOperation(operation) {
		if !AskForConfirmation("🚨 This is a destructive operation. Final confirmation?") {
			color.Yellow("❌ Operation cancelled")
			return nil
		}
	}

	// Execute the command
	color.Green("✅ Executing git operation...")
	return gm.sandbox.WrapCommand(finalCommand, gm.execConfig, gm.env)
}

// executeMultiCommandOperation handles complex git operations step by step
func (gm *GitManager) executeMultiCommandOperation(operation *GitOperation) error {
	// Parse the multi-command operation
	commands := gm.parseMultiCommands(operation.Command)

	if len(commands) == 0 {
		return fmt.Errorf("no commands to execute")
	}

	color.Cyan("🔧 This operation will execute %d commands:", len(commands))
	for i, cmd := range commands {
		color.Cyan("  %d. %s", i+1, cmd)
	}
	fmt.Println()

	// Get target branch if needed
	targetBranch := ""
	if strings.Contains(operation.Command, "${BRANCH}") {
		branch, err := gm.getTargetBranch()
		if err != nil {
			return err
		}
		targetBranch = branch
		color.Green("🎯 Target branch: %s", targetBranch)
	}

	// Final confirmation
	if !AskForConfirmation("Execute these commands sequentially?") {
		color.Yellow("❌ Operation cancelled")
		return nil
	}

	// Execute commands one by one
	for i, rawCommand := range commands {
		// Replace branch placeholder
		command := strings.ReplaceAll(rawCommand, "${BRANCH}", targetBranch)

		color.Blue("\n📝 Step %d/%d: %s", i+1, len(commands), command)

		// SPECIAL HANDLING: For commit step, use a completely different approach
		if i == len(commands)-1 && strings.Contains(rawCommand, "${COMMIT_CMD}") {
			// This is the commit step - handle it specially
			if err := gm.executeCommitStep(targetBranch); err != nil {
				color.Red("❌ Commit failed: %v", err)
				color.Yellow("💡 Operation incomplete. Check git status.")
				return err
			}
			color.Green("✅ Step %d completed", i+1)
			continue
		}

		if err := gm.sandbox.WrapCommand(command, gm.execConfig, gm.env); err != nil {
			color.Red("❌ Command failed at step %d: %v", i+1, err)
			color.Yellow("💡 Operation incomplete. Check git status.")
			return err
		}

		color.Green("✅ Step %d completed", i+1)
	}

	color.Green("🎉 All commands completed successfully!")
	return nil
}

// executeCommitStep handles the commit step without shell escaping issues
func (gm *GitManager) executeCommitStep(targetBranch string) error {
	color.Cyan("💭 Commit options:")
	color.Cyan("  1. Use default message ('Merge %s with squash')", targetBranch)
	color.Cyan("  2. Enter custom message")
	color.Cyan("  3. Open editor for message")

	var choice string
	color.Cyan("Choose option (1/2/3): ")
	fmt.Scanln(&choice)

	switch strings.TrimSpace(choice) {
	case "1":
		// Use default message with NO SHELL ESCAPING - let git handle it internally
		return gm.executeCommitWithMessage(targetBranch, fmt.Sprintf("Merge %s with squash", targetBranch))
	case "2":
		// Get custom message
		color.Cyan("Enter commit message: ")
		var message string
		fmt.Scanln(&message)
		message = strings.TrimSpace(message)
		if message == "" {
			message = fmt.Sprintf("Merge %s with squash", targetBranch)
		}
		return gm.executeCommitWithMessage(targetBranch, message)
	case "3":
		// Let git open the editor
		color.Blue("📝 Opening commit editor...")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	default:
		// Default to editor
		color.Blue("📝 Opening commit editor...")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	}
}

// executeCommitWithMessage executes git commit without shell escaping issues
func (gm *GitManager) executeCommitWithMessage(targetBranch string, message string) error {
	color.Blue("📝 Committing with message: %s", message)

	// Use a temporary file for the commit message to avoid shell escaping entirely
	tempFile, err := os.CreateTemp("", "helix-commit-*.txt")
	if err != nil {
		color.Yellow("⚠️  Could not create temp file, using git editor instead")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	}
	defer os.Remove(tempFile.Name())

	// Write message to temp file
	if _, err := tempFile.WriteString(message); err != nil {
		color.Yellow("⚠️  Could not write to temp file, using git editor instead")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	}
	tempFile.Close()

	// Use the temp file for commit message
	commitCmd := fmt.Sprintf("git commit -F %s", tempFile.Name())
	return gm.sandbox.WrapCommand(commitCmd, gm.execConfig, gm.env)
}

// parseMultiCommands splits a multi-command string into individual commands
func (gm *GitManager) parseMultiCommands(multiCommand string) []string {
	// Split by semicolons first (more reliable than && for complex commands)
	commands := strings.Split(multiCommand, ";")

	var result []string
	for _, cmd := range commands {
		cmd = strings.TrimSpace(cmd)
		if cmd != "" {
			result = append(result, cmd)
		}
	}
	return result
}

// isMultiCommandOperation checks if an operation requires multiple commands
func (gm *GitManager) isMultiCommandOperation(operation *GitOperation) bool {
	return strings.Contains(operation.Command, ";") ||
		strings.Contains(operation.Command, "&&") ||
		strings.Contains(operation.Command, "${BRANCH}")
}

// getTargetBranch prompts user for target branch with suggestions
func (gm *GitManager) getTargetBranch() (string, error) {
	branches, err := gm.getAvailableBranches()
	if err != nil {
		color.Yellow("⚠️  Could not fetch available branches")
	} else if len(branches) > 0 {
		color.Cyan("🌿 Available branches:")
		for i, branch := range branches {
			if i < 10 { // Show first 10 branches
				color.Cyan("   %s", branch)
			}
		}
	}

	color.Cyan("\n🔍 Enter target branch name: ")
	var branch string
	fmt.Scanln(&branch)
	branch = strings.TrimSpace(branch)

	if branch == "" {
		return "", fmt.Errorf("no branch specified")
	}

	return branch, nil
}

// isDestructiveOperation checks if an operation is potentially destructive
func (gm *GitManager) isDestructiveOperation(operation *GitOperation) bool {
	dangerousPatterns := []string{
		"checkout --theirs",
		"reset --hard",
		"clean -fd",
		"push --force",
		"branch -D",
		"reset --soft",
	}

	for _, pattern := range dangerousPatterns {
		if strings.Contains(operation.Command, pattern) {
			return true
		}
	}
	return false
}

// handleAIGitRequest uses AI for other git operations
func (gm *GitManager) handleAIGitRequest(request string) error {
	// Get current branch info for context
	currentBranch, _ := gm.getCurrentBranch()

	prompt := fmt.Sprintf(`You are a git expert. Provide a single git command for: "%s"

Current context:
- Working directory: %s
- Current branch: %s

Rules:
- Output ONLY the git command
- No explanations, no markdown, no backticks
- Make it safe and appropriate
- Include necessary flags but avoid destructive options unless clearly requested

Command:`, request, gm.workingDir, currentBranch)

	color.Blue("🤖 Generating git command with AI...")
	response, err := RunModel(prompt)
	if err != nil {
		return fmt.Errorf("AI git command generation failed: %w", err)
	}

	command := ExtractCommand(response)
	if command == "" {
		return fmt.Errorf("AI didn't generate a valid git command")
	}

	color.Cyan("💡 Generated command: %s", command)

	// Basic git command validation
	if !strings.HasPrefix(command, "git ") {
		command = "git " + command
	}

	// Show current directory context
	color.Blue("📍 Executing in: %s", gm.workingDir)

	// Ask for confirmation
	if AskForConfirmation("Execute this git command?") {
		return gm.sandbox.WrapCommand(command, gm.execConfig, gm.env)
	}

	color.Yellow("💡 Command ready: %s", command)
	return nil
}

// CommonGitOperations returns a list of common git operations for autocomplete
func (gm *GitManager) CommonGitOperations() map[string]string {
	return map[string]string{
		"merge squash accept all": "Merge branch with squash and accept all incoming changes",
		"merge squash":            "Merge branch with squash commits",
		"undo last commit":        "Reset last commit but keep changes",
		"clean untracked":         "Remove untracked files and directories",
		"change last commit":      "Amend the most recent commit",
		"stash all":               "Stash all changes including untracked files",
		"status":                  "Show repository status",
		"log":                     "Show commit history",
		"diff":                    "Show changes",
	}
}

// internal/commands/git.go

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"helix/internal/ai"
	"helix/internal/shell"
	"helix/internal/utils"

	"github.com/fatih/color"
)

// GitManager handles git-specific operations with AI assistance
type GitManager struct {
	env        shell.Env
	execConfig ExecuteConfig
	workingDir string
	sandbox    *DirectorySandbox
}

// NewGitManager creates a new Git manager
func NewGitManager(env shell.Env, execConfig ExecuteConfig, sandbox *DirectorySandbox) *GitManager {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}

	return &GitManager{
		env:        env,
		execConfig: execConfig,
		workingDir: wd,
		sandbox:    sandbox,
	}
}

// GitOperation represents a git operation with safety checks
type GitOperation struct {
	Description  string
	Command      string
	Confirmation string
	Risks        []string
}

// HandleGitRequest processes natural language git requests from /git
func (gm *GitManager) HandleGitRequest(request string) error {
	request = strings.ToLower(strings.TrimSpace(request))

	color.Blue("🔧 Processing git request: %s", request)

	// First, validate we're in a git repository
	if !gm.isGitRepository() {
		color.Red("Not a git repository")
		color.Yellow("Current directory: %s", gm.workingDir)
		color.Yellow("Navigate to a git repository first or run 'git init'")
		return fmt.Errorf("not a git repository")
	}

	// Check for common complex git operations with hand-crafted flows
	if operation := gm.detectComplexGitOperation(request); operation != nil {
		return gm.executeGitOperation(operation)
	}

	// Fall back to AI for other git requests (shell-style git commands)
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

// detectComplexGitOperation identifies common complex git workflows for /git
func (gm *GitManager) detectComplexGitOperation(request string) *GitOperation {
	// Merge with squash and accept all incoming
	if strings.Contains(request, "merge") && strings.Contains(request, "squash") &&
		(strings.Contains(request, "accept all") || strings.Contains(request, "incoming")) {
		return &GitOperation{
			Description: "Merge branch with squash and accept all incoming changes",
			Command:     "git merge --squash ${BRANCH}; git checkout --theirs .; git add .; ${COMMIT_CMD}",
			Confirmation: "This will:\n• Squash all commits from the branch into one\n" +
				"• Accept ALL incoming changes (overwrite local conflicts)\n" +
				"• Create a new commit with a single message",
			Risks: []string{
				"Permanently overwrites local changes in case of conflicts",
				"Loses individual commit history from the merged branch",
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

// executeGitOperation safely executes a git operation with confirmation (for /git)
func (gm *GitManager) executeGitOperation(operation *GitOperation) error {
	color.Cyan("\n Operation: %s", operation.Description)
	color.Yellow(" Command: %s", operation.Command)

	// Show current directory and branch info
	currentBranch, err := gm.getCurrentBranch()
	if err == nil {
		color.Blue("Current branch: %s", currentBranch)
	}
	color.Blue("Repository: %s", gm.workingDir)

	// Show risks
	if len(operation.Risks) > 0 {
		color.Red("--Risks:")
		for _, risk := range operation.Risks {
			color.Red("   • %s", risk)
		}
		fmt.Println()
	}

	// Get confirmation
	if !AskForConfirmation(operation.Confirmation) {
		color.Yellow("Operation cancelled")
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
		color.Green("Target branch: %s", branch)
	}

	// Final confirmation for destructive operations
	if gm.isDestructiveOperation(operation) {
		if !AskForConfirmation("This is a destructive operation. Final confirmation?") {
			color.Yellow("Operation cancelled")
			return nil
		}
	}

	// Execute the command
	color.Green("Executing git operation...")
	return gm.sandbox.WrapCommand(finalCommand, gm.execConfig, gm.env)
}

// executeMultiCommandOperation handles complex git operations step by step
func (gm *GitManager) executeMultiCommandOperation(operation *GitOperation) error {
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
		color.Green("Target branch: %s", targetBranch)
	}

	// Final confirmation
	if !AskForConfirmation("Execute these commands sequentially?") {
		color.Yellow("Operation cancelled")
		return nil
	}

	// Execute commands one by one
	for i, rawCommand := range commands {
		// Replace branch placeholder
		command := strings.ReplaceAll(rawCommand, "${BRANCH}", targetBranch)

		color.Blue("\n Step %d/%d: %s", i+1, len(commands), command)

		// SPECIAL HANDLING: For commit step, use a completely different approach
		if i == len(commands)-1 && strings.Contains(rawCommand, "${COMMIT_CMD}") {
			// This is the commit step - handle it specially
			if err := gm.executeCommitStep(targetBranch); err != nil {
				color.Red("Commit failed: %v", err)
				color.Yellow("Operation incomplete. Check git status.")
				return err
			}
			color.Green("Step %d completed", i+1)
			continue
		}

		if err := gm.sandbox.WrapCommand(command, gm.execConfig, gm.env); err != nil {
			color.Red("Command failed at step %d: %v", i+1, err)
			color.Yellow("Operation incomplete. Check git status.")
			return err
		}

		color.Green("Step %d completed", i+1)
	}

	color.Green("All commands completed successfully!")
	return nil
}

// executeCommitStep handles the commit step without shell escaping issues.
func (gm *GitManager) executeCommitStep(targetBranch string) error {
	color.Cyan("Commit options:")
	color.Cyan(" 1. Use default message ('Merge %s with squash')", targetBranch)
	color.Cyan(" 2. Enter custom message")
	color.Cyan(" 3. Open editor for message")

	choice := strings.TrimSpace(AskLine("Choose option (1/2/3)"))

	switch choice {
	case "1":
		return gm.executeCommitWithMessage(
			targetBranch,
			fmt.Sprintf("Merge %s with squash", targetBranch),
		)
	case "2":
		message := strings.TrimSpace(AskLine("Enter commit message"))
		if message == "" {
			message = fmt.Sprintf("Merge %s with squash", targetBranch)
		}

		return gm.executeCommitWithMessage(targetBranch, message)
	case "3":
		color.Blue("Opening commit editor...")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	default:
		color.Blue("Opening commit editor...")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	}
}

// executeCommitWithMessage executes git commit using a relative commit-message file
// to avoid absolute paths (which are blocked by the sandbox).
func (gm *GitManager) executeCommitWithMessage(targetBranch string, message string) error {
	color.Blue("Committing with message: %s", message)

	// Safety: skip commit if nothing is staged
	hasChanges, err := gm.hasStagedChanges()
	if err != nil {
		color.Yellow("Could not detect staged changes: %v", err)
	} else if !hasChanges {
		color.Yellow("No staged changes detected; skipping git commit.")
		return nil
	}

	relPath := ".helix-commit-msg.txt"
	absPath := filepath.Join(gm.workingDir, relPath)

	if err := os.WriteFile(absPath, []byte(message), 0o600); err != nil {
		color.Yellow("Could not write commit message file, using git editor instead")
		return ExecuteCommand("git commit", gm.execConfig, gm.env)
	}
	defer func() { _ = os.Remove(absPath) }()

	// Use the relative path in the command so there is no absolute path for the sandbox to reject.
	commitCmd := fmt.Sprintf("git commit -F %s", relPath)
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

// getTargetBranch prompts user for target branch with suggestions.
func (gm *GitManager) getTargetBranch() (string, error) {
	branches, err := gm.getAvailableBranches()
	if err != nil {
		color.Yellow("Could not fetch available branches")
	} else if len(branches) > 0 {
		color.Cyan("Available branches:")
		for i, branch := range branches {
			if i < 10 {
				color.Cyan("   %s", branch)
			}
		}
	}

	color.Cyan("")
	branch := AskLine("Enter target branch name")
	branch = strings.TrimSpace(branch)

	if branch == "" {
		return "", fmt.Errorf("no branch specified")
	}

	return branch, nil
}

// isDestructiveOperation checks if an operation is potentially destructive (for /git flows)
func (gm *GitManager) isDestructiveOperation(operation *GitOperation) bool {
	dangerousPatterns := []string{
		"checkout --theirs",
		"reset --hard",
		"clean -fd",
		"clean -fdx",
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

// handleAIGitRequest uses AI for other git operations in /git mode
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

	color.Blue("Generating git command with AI...")
	response, err := ai.RunModel(prompt)
	if err != nil {
		return fmt.Errorf("AI git command generation failed: %w", err)
	}

	command := utils.ExtractCommand(response)
	if command == "" {
		return fmt.Errorf("AI didn't generate a valid git command")
	}

	color.Cyan("Generated command: %s", command)

	// Basic git command validation
	if !strings.HasPrefix(command, "git ") {
		command = "git " + command
	}

	// Show current directory context
	color.Blue("Executing in: %s", gm.workingDir)

	// Ask for confirmation
	if AskForConfirmation("Execute this git command?") {
		return gm.sandbox.WrapCommand(command, gm.execConfig, gm.env)
	}

	color.Yellow("Command ready: %s", command)
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

// ExecutePlannedAction executes a git action coming from the planner
// without asking AI again or generating raw shell commands.
//
// This is the critical path for Agent Mode. It supports both safe
// and dangerous actions. Dangerous actions require typed confirmation.
func (gm *GitManager) ExecutePlannedAction(action string, args map[string]string) error {
	action = strings.ToLower(strings.TrimSpace(action))
	if args == nil {
		args = map[string]string{}
	}

	// 1) Structural safety check (no shell injection via args)
	if err := isPlannerGitActionSafe(action, args); err != nil {
		color.Red("Git safety violation: %v", err)
		return err
	}

	switch action {

	// -------- SAFE ACTIONS --------

	case "commit":
		msg := strings.TrimSpace(args["message"])
		if msg == "" {
			msg = "Helix: automated commit"
		}
		color.Blue("Git commit (planner): %s", msg)
		return gm.executeCommitWithMessage("<planner>", msg)

	case "tag":
		nameRaw := strings.TrimSpace(args["name"])
		name, err := sanitizeGitName(nameRaw)
		if err != nil {
			return fmt.Errorf("invalid tag name %q: %w", nameRaw, err)
		}
		color.Blue("Git tag (planner): %s", name)
		cmd := fmt.Sprintf("git tag %s", name)
		return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)

	case "add":
		paths := strings.TrimSpace(args["paths"])
		if paths == "" {
			return fmt.Errorf("git add action missing 'paths'")
		}
		color.Blue("Git add (planner): %s", paths)
		cmd := fmt.Sprintf("git add %s", paths)
		return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)

	case "checkout":
		branchRaw := strings.TrimSpace(args["branch"])
		if branchRaw == "" {
			return fmt.Errorf("git checkout action missing 'branch'")
		}
		branch, err := sanitizeGitName(branchRaw)
		if err != nil {
			return fmt.Errorf("invalid branch name %q: %w", branchRaw, err)
		}
		color.Blue("Git checkout (planner): %s", branch)
		cmd := fmt.Sprintf("git checkout %s", branch)
		return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)

	case "create-branch":
		branchRaw := strings.TrimSpace(args["branch"])
		if branchRaw == "" {
			return fmt.Errorf("create-branch action missing 'branch'")
		}
		branch, err := sanitizeGitName(branchRaw)
		if err != nil {
			return fmt.Errorf("invalid branch name %q: %w", branchRaw, err)
		}
		color.Blue("Git create branch (planner): %s", branch)
		cmd := fmt.Sprintf("git checkout -b %s", branch)
		return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)

	// -------- DANGEROUS ACTIONS (Option C) --------

	case "push":
		return gm.executePlannerPush(args)

	case "reset-hard":
		return gm.executePlannerResetHard(args)

	case "clean":
		return gm.executePlannerClean(args)

	case "delete-branch":
		return gm.executePlannerDeleteBranch(args)

	default:
		return fmt.Errorf("unsupported git planner action: %s", action)
	}
}

// executePlannerPush handles both normal push and force push.
func (gm *GitManager) executePlannerPush(args map[string]string) error {
	remote := strings.TrimSpace(args["remote"])
	if remote == "" {
		remote = "origin"
	}

	branch := strings.TrimSpace(args["branch"])
	if branch == "" {
		// fallback to current branch
		if cur, err := gm.getCurrentBranch(); err == nil && cur != "" {
			branch = cur
		}
	}
	if branch == "" {
		return fmt.Errorf("git push action missing 'branch' and could not detect current branch")
	}

	force := strings.EqualFold(args["force"], "true") ||
		strings.EqualFold(args["force"], "yes") ||
		args["force"] == "--force" ||
		args["force"] == "-f"

	// Normal push
	if !force {
		color.Blue("Git push (planner): %s %s", remote, branch)
		if !AskForConfirmation(fmt.Sprintf("Push branch %q to remote %q?", branch, remote)) {
			color.Yellow("Push cancelled")
			return nil
		}
		cmd := fmt.Sprintf("git push %s %s", remote, branch)
		return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)
	}

	// Force push — HIGH DANGER
	color.Red("FORCE PUSH REQUESTED")
	color.Red("Remote: %s", remote)
	color.Red("Branch: %s", branch)
	color.Yellow("This will overwrite history on the remote branch and can cause data loss for others.")

	if !confirmDangerousAction("force push", "YES, FORCE PUSH") {
		return fmt.Errorf("force push cancelled by user")
	}

	cmd := fmt.Sprintf("git push %s %s --force", remote, branch)
	return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)
}

// executePlannerResetHard handles "reset-hard" action.
func (gm *GitManager) executePlannerResetHard(args map[string]string) error {
	target := strings.TrimSpace(args["target"])
	if target == "" {
		target = "HEAD"
	}

	color.Red("HARD RESET REQUESTED")
	color.Red("Target: %s", target)
	color.Yellow("This will discard local changes and reset the branch to the specified target.")

	if !confirmDangerousAction("reset --hard", "YES, RESET HARD") {
		return fmt.Errorf("reset --hard cancelled by user")
	}

	cmd := fmt.Sprintf("git reset --hard %s", target)
	return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)
}

// executePlannerClean handles "clean" action (e.g. -fd or -fdx).
func (gm *GitManager) executePlannerClean(args map[string]string) error {
	mode := strings.ToLower(strings.TrimSpace(args["mode"]))
	flags := "-fd"
	description := "remove untracked files and directories"

	if mode == "all" || mode == "force-all" || strings.EqualFold(args["x"], "true") {
		flags = "-fdx"
		description = "remove untracked files, directories, and ignored files"
	}

	color.Red("GIT CLEAN REQUESTED")
	color.Red("Mode: git clean %s", flags)
	color.Yellow("This will %s. Deleted files cannot be recovered from git.", description)

	if !confirmDangerousAction("git clean", "YES, CLEAN WORKTREE") {
		return fmt.Errorf("git clean cancelled by user")
	}

	cmd := fmt.Sprintf("git clean %s", flags)
	return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)
}

// executePlannerDeleteBranch handles "delete-branch" action.
func (gm *GitManager) executePlannerDeleteBranch(args map[string]string) error {
	branchRaw := strings.TrimSpace(args["branch"])
	if branchRaw == "" {
		return fmt.Errorf("delete-branch action missing 'branch'")
	}
	branch, err := sanitizeGitName(branchRaw)
	if err != nil {
		return fmt.Errorf("invalid branch name %q: %w", branchRaw, err)
	}

	// Do not allow deleting current branch
	if cur, err := gm.getCurrentBranch(); err == nil && cur != "" && cur == branch {
		return fmt.Errorf("refusing to delete the currently checked-out branch %q", branch)
	}

	// Optional: protect super-common mains
	if branch == "main" || branch == "master" {
		color.Red("Attempt to delete primary branch %q", branch)
		if !confirmDangerousAction("delete main branch", "YES, DELETE MAIN") {
			return fmt.Errorf("deletion of main branch cancelled")
		}
	} else {
		color.Red("GIT BRANCH DELETE REQUESTED")
		color.Red("Branch: %s", branch)
		if !confirmDangerousAction("delete branch", "YES, DELETE BRANCH") {
			return fmt.Errorf("git branch delete cancelled by user")
		}
	}

	cmd := fmt.Sprintf("git branch -D %s", branch)
	return gm.sandbox.WrapCommand(cmd, gm.execConfig, gm.env)
}

// hasStagedChanges returns true if there are staged changes.
func (gm *GitManager) hasStagedChanges() (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet")
	cmd.Dir = gm.workingDir
	err := cmd.Run()
	if err == nil {
		// exit code 0: no staged changes
		return false, nil
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == 1 {
			// exit code 1: there are staged changes
			return true, nil
		}
	}

	return false, err
}

// isPlannerGitActionSafe validates planner→git actions structurally.
// It does NOT block actions by type (Option C); it only prevents obvious injection.
func isPlannerGitActionSafe(action string, args map[string]string) error {
	action = strings.ToLower(strings.TrimSpace(action))

	if action == "" {
		return fmt.Errorf("empty git action")
	}

	for k, v := range args {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// No absolute paths
		if strings.HasPrefix(v, "/") {
			return fmt.Errorf("unsafe git argument for %s: %q is an absolute path", k, v)
		}
		// No shell meta characters in names
		if strings.ContainsAny(v, ";|&><`$") {
			return fmt.Errorf("unsafe characters in git argument %s=%q", k, v)
		}
		// Very basic path traversal guard
		if strings.Contains(v, "..") {
			return fmt.Errorf("unsafe git argument %s=%q: contains '..'", k, v)
		}
	}

	return nil
}

// sanitizeGitName ensures git branch/tag names are safe identifiers.
func sanitizeGitName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("empty git name")
	}
	if strings.ContainsAny(name, " /\\:*?\"<>|;&$`") {
		return "", fmt.Errorf("illegal characters in git name: %s", name)
	}
	return name, nil
}

// confirmDangerousAction asks the user to type an exact phrase to proceed.
func confirmDangerousAction(label, requiredPhrase string) bool {
	color.Red("This is a HIGH-RISK git operation: %s", label)
	return AskTypedConfirmation(label, requiredPhrase)
}

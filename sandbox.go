// sandbox.go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fatih/color"
)

// SandboxMode defines the level of directory restriction
type SandboxMode int

const (
	SandboxDisabled SandboxMode = iota
	SandboxCurrentDir
	SandboxStrict
)

// DirectorySandbox manages execution restrictions
type DirectorySandbox struct {
	allowedDir  string
	mode        SandboxMode
	originalDir string
}

// NewDirectorySandbox creates a new sandbox instance
func NewDirectorySandbox() *DirectorySandbox {
	currentDir, err := os.Getwd()
	if err != nil {
		currentDir = "." // Fallback
	}

	return &DirectorySandbox{
		allowedDir:  currentDir,
		mode:        SandboxCurrentDir,
		originalDir: currentDir,
	}
}

// ValidateCommand checks if a command is allowed within the sandbox
func (ds *DirectorySandbox) ValidateCommand(command string) (bool, string) {
	if ds.mode == SandboxDisabled {
		return true, "" // No restrictions
	}

	command = strings.ToLower(command)

	// Always block absolute path traversal attempts
	if ds.containsAbsolutePathTraversal(command) {
		return false, "Command contains absolute path traversal"
	}

	// Check for attempts to escape the sandbox directory
	if ds.containsDirectoryEscape(command) {
		return false, "Command attempts to escape sandbox directory"
	}

	// Check for dangerous operations outside sandbox
	if ds.containsDangerousExternalOperations(command) {
		return false, "Command performs dangerous operations outside sandbox"
	}

	return true, ""
}

// containsAbsolutePathTraversal checks for absolute path usage
func (ds *DirectorySandbox) containsAbsolutePathTraversal(command string) bool {
	// Match absolute paths (Unix: /, Windows: C:\, D:\, etc.)
	absolutePathPatterns := []*regexp.Regexp{
		regexp.MustCompile(`\s/(?:[^/]\S*)?`), // Unix absolute paths
		regexp.MustCompile(`(?i)\s[a-z]:\\`),  // Windows drive letters (case-insensitive)
		regexp.MustCompile(`rm\s+-rf\s+/`),    // Dangerous rm with root
		regexp.MustCompile(`chmod\s+.*\s+/`),  // chmod on root
		regexp.MustCompile(`chown\s+.*\s+/`),  // chown on root
	}

	for _, pattern := range absolutePathPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}

	return false
}

// containsDirectoryEscape checks for attempts to escape current directory
func (ds *DirectorySandbox) containsDirectoryEscape(command string) bool {
	// Patterns that attempt to move up directory hierarchy
	escapePatterns := []string{
		"cd ..", "cd ../", "cd ..\\",
		"rm -rf ../", "rm -rf ..\\",
		"../", "..\\",
	}

	for _, pattern := range escapePatterns {
		if strings.Contains(command, pattern) {
			// Allow if it's just checking parent, but not operating on it
			if ds.isJustChecking(command) {
				continue
			}
			return true
		}
	}

	return false
}

// containsDangerousExternalOperations checks for dangerous ops outside sandbox
func (ds *DirectorySandbox) containsDangerousExternalOperations(command string) bool {
	dangerousCommands := []string{
		"rm -rf", "chmod", "chown", "mv ", "cp ", "dd ",
		"format", "mkfs", "fdisk",
	}

	// If command contains dangerous operations with relative paths that escape
	for _, dangerousCmd := range dangerousCommands {
		if strings.Contains(command, dangerousCmd) {
			// Check if it's operating outside current directory
			if ds.operatesOutsideCurrentDir(command) {
				return true
			}
		}
	}

	return false
}

// isJustChecking checks if command is just inspecting rather than modifying
func (ds *DirectorySandbox) isJustChecking(command string) bool {
	safePatterns := []string{
		"ls", "find", "grep", "cat", "head", "tail", "file",
		"stat", "du", "df", "pwd", "echo", "print",
	}

	for _, pattern := range safePatterns {
		if strings.HasPrefix(command, pattern) {
			return true
		}
	}
	return false
}

// operatesOutsideCurrentDir checks if command operates outside allowed directory
func (ds *DirectorySandbox) operatesOutsideCurrentDir(command string) bool {
	// Extract file/directory arguments from command
	args := ds.extractFileArguments(command)

	for _, arg := range args {
		if ds.isOutsideSandbox(arg) {
			return true
		}
	}

	return false
}

// extractFileArguments extracts potential file/directory arguments from command
func (ds *DirectorySandbox) extractFileArguments(command string) []string {
	var files []string

	// Split command into words
	words := strings.Fields(command)

	// Skip the command itself and flags, look for file-like arguments
	for i := 1; i < len(words); i++ {
		word := words[i]

		// Skip flags
		if strings.HasPrefix(word, "-") {
			continue
		}

		// Skip common non-file arguments
		if ds.isCommonNonFileArgument(word) {
			continue
		}

		// This might be a file/directory argument
		files = append(files, word)
	}

	return files
}

// isCommonNonFileArgument checks if argument is likely not a file
func (ds *DirectorySandbox) isCommonNonFileArgument(arg string) bool {
	nonFilePatterns := []string{
		"yes", "no", "true", "false", "0", "1",
		"localhost", "127.0.0.1", "0.0.0.0",
		"http://", "https://", "ftp://",
	}

	for _, pattern := range nonFilePatterns {
		if strings.Contains(arg, pattern) {
			return true
		}
	}

	return false
}

// isOutsideSandbox checks if a path is outside the allowed directory
func (ds *DirectorySandbox) isOutsideSandbox(path string) bool {
	// Clean and resolve the path
	cleanPath := filepath.Clean(path)

	// If it's a relative path, make it absolute relative to current dir
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(ds.allowedDir, cleanPath)
	}

	// Check if the resolved path is within the allowed directory
	relativePath, err := filepath.Rel(ds.allowedDir, cleanPath)
	if err != nil {
		return true // Error means it's likely outside
	}

	// If relative path starts with .., it's outside
	return strings.HasPrefix(relativePath, "..")
}

// SetMode changes the sandbox restriction level
func (ds *DirectorySandbox) SetMode(mode SandboxMode) {
	ds.mode = mode
	color.Yellow("🔒 Sandbox mode set to: %s", ds.ModeString())
}

// GetMode returns the current sandbox mode
func (ds *DirectorySandbox) GetMode() SandboxMode {
	return ds.mode
}

// ModeString returns a human-readable mode description
func (ds *DirectorySandbox) ModeString() string {
	switch ds.mode {
	case SandboxDisabled:
		return "Disabled (no restrictions)"
	case SandboxCurrentDir:
		return "Current Directory Only"
	case SandboxStrict:
		return "Strict (current dir + subdirs only)"
	default:
		return "Unknown"
	}
}

// ChangeDirectory safely changes the current working directory
func (ds *DirectorySandbox) ChangeDirectory(newDir string) error {
	if ds.mode == SandboxDisabled {
		return os.Chdir(newDir)
	}

	// Clean the path
	cleanDir := filepath.Clean(newDir)

	// If relative path, make absolute relative to current
	if !filepath.IsAbs(cleanDir) {
		cleanDir = filepath.Join(ds.allowedDir, cleanDir)
	}

	// Check if the new directory is within sandbox
	if ds.isOutsideSandbox(cleanDir) {
		return fmt.Errorf("cannot change to directory outside sandbox: %s", newDir)
	}

	// Change directory
	if err := os.Chdir(cleanDir); err != nil {
		return err
	}

	// Update allowed directory to new location
	ds.allowedDir = cleanDir
	color.Green("📁 Changed to directory: %s", cleanDir)

	return nil
}

// GetCurrentDirectory returns the current sandboxed directory
func (ds *DirectorySandbox) GetCurrentDirectory() string {
	return ds.allowedDir
}

// ResetToOriginal resets to the original directory
func (ds *DirectorySandbox) ResetToOriginal() error {
	if ds.mode != SandboxDisabled && ds.isOutsideSandbox(ds.originalDir) {
		return fmt.Errorf("cannot reset to original directory outside sandbox")
	}

	if err := os.Chdir(ds.originalDir); err != nil {
		return err
	}

	ds.allowedDir = ds.originalDir
	color.Green("📁 Reset to original directory: %s", ds.originalDir)
	return nil
}

// WrapCommand wraps a command with sandbox safety checks
func (ds *DirectorySandbox) WrapCommand(command string, execConfig ExecuteConfig, env Env) error {
	// Validate command against sandbox rules
	if valid, reason := ds.ValidateCommand(command); !valid {
		return fmt.Errorf("sandbox violation: %s", reason)
	}

	// Execute the command with current directory context
	return ExecuteCommand(command, execConfig, env)
}

// PrintStatus shows current sandbox status
func (ds *DirectorySandbox) PrintStatus() {
	color.Cyan("🔒 Sandbox Status:")
	color.Cyan("  Mode: %s", ds.ModeString())
	color.Cyan("  Allowed Directory: %s", ds.allowedDir)
	color.Cyan("  Original Directory: %s", ds.originalDir)

	// Show current working directory for comparison
	currentDir, _ := os.Getwd()
	color.Cyan("  Current Working Directory: %s", currentDir)
}

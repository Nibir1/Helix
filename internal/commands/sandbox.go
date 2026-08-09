// internal/commands/sandbox.go
package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"helix/internal/shell"

	"github.com/fatih/color"
)

// SandboxMode defines the restriction level
type SandboxMode int

const (
	SandboxDisabled SandboxMode = iota
	SandboxCurrentDir
	SandboxStrict
)

// DirectorySandbox with Mode C (read-only outside sandbox)
type DirectorySandbox struct {
	allowedDir  string
	mode        SandboxMode
	originalDir string
}

// evalSymlinksSafe wraps filepath.EvalSymlinks with panic recovery.
//
// The Go standard library's Windows symlink resolver (symlink_windows.go,
// toNorm) has a documented history of panicking on malformed inputs such
// as bare UNC prefixes and truncated drive specs (golang/go#63703,
// golang/go#40966). ValidateSafePath feeds caller-controlled strings into
// symlink resolution, so a hostile or fuzzed path could crash the whole
// shell on Windows.
//
// A panic during resolution is converted into an error so the sandbox
// FAILS CLOSED: an unresolvable path is simply not symlink-validated and
// still must pass the lexical root-prefix check.
//
// Args:
//   - path: candidate path to resolve.
//
// Returns:
//   - string: resolved path ("" when a panic was recovered).
//   - error: resolution error, or a synthesized error for a recovered panic.
//
// Complexity: O(len(path)) plus filesystem symlink traversal.
func evalSymlinksSafe(path string) (resolved string, err error) {
	defer func() {
		if r := recover(); r != nil {
			resolved = ""
			err = fmt.Errorf("path resolution aborted (treated as unsafe): %v", r)
		}
	}()
	return filepath.EvalSymlinks(path)
}

// NewDirectorySandbox creates default sandbox
func NewDirectorySandbox() *DirectorySandbox {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	// FIX: Immediately resolve symlinks to establish the "Canonical Root"
	// (panic-safe: Windows stdlib can panic on malformed paths).
	realCwd, err := evalSymlinksSafe(cwd)
	if err == nil {
		cwd = realCwd
	}
	return &DirectorySandbox{
		allowedDir:  cwd,
		mode:        SandboxCurrentDir,
		originalDir: cwd,
	}
}

// GetCurrentDirectory returns the sandbox root.
func (ds *DirectorySandbox) GetCurrentDirectory() string {
	return ds.allowedDir
}

// ================================================================
// MODE C CORE LOGIC
// Allow READ-ONLY absolute paths anywhere,
// but MODIFY/WRITE actions are blocked outside sandbox.
// ================================================================

// ValidateCommand checks if a command is allowed under sandbox rules
func (ds *DirectorySandbox) ValidateCommand(command string) (bool, string) {
	if ds.mode == SandboxDisabled {
		return true, ""
	}

	commandLower := strings.ToLower(command)

	// 1. Detect dangerous write/delete/edit operations
	if ds.isDangerousWriteOperation(commandLower) {
		args := ds.extractFileArguments(commandLower)

		for _, arg := range args {
			if arg == "" {
				continue
			}

			if _, err := ds.ValidateSafePath(arg); err != nil {
				return false, fmt.Sprintf(
					"sandbox violation: write/delete/edit operation outside sandbox: %s", arg)
			}
		}
	}

	// 2. Detect directory escape attempts (../ etc.)
	if ds.containsDirectoryEscape(commandLower) {
		return false, "command attempts directory escape"
	}

	return true, ""
}

// ================================================================
// ROBUST PATH VALIDATION
// ================================================================

// ValidateSafePath checks if a target path is inside the sandbox.
// It handles Symlinks, Case-Sensitivity (macOS/Windows), and Non-Existent files.
// FIX: Resolves relative paths against the CURRENT working directory,
// but validates them against the ORIGINAL sandbox root (the jail cell).
func (ds *DirectorySandbox) ValidateSafePath(targetPath string) (string, error) {
	if ds.mode == SandboxDisabled {
		return targetPath, nil
	}
	// 1. Absolutize against the CURRENT working directory (not just sandbox root)
	if !filepath.IsAbs(targetPath) {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = ds.allowedDir // Fallback to cached root if os.Getwd fails
		}
		targetPath = filepath.Join(cwd, targetPath)
	}
	// 2. Clean
	cleanTarget := filepath.Clean(targetPath)
	// 3. Resolve Symlinks (Target) — panic-safe on Windows.
	realTarget, err := evalSymlinksSafe(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist; check parent directory instead
			parent := filepath.Dir(cleanTarget)
			realParent, perr := evalSymlinksSafe(parent)
			if perr == nil {
				realTarget = filepath.Join(realParent, filepath.Base(cleanTarget))
			} else {
				realTarget = cleanTarget
			}
		} else {
			realTarget = cleanTarget
		}
	}
	// 4. Resolve Symlinks (Root) - Ensure base comparison is accurate
	realRoot, err := evalSymlinksSafe(ds.allowedDir)
	if err != nil {
		realRoot = ds.allowedDir
	}
	// 5. Case-Insensitive Comparison (fixes /Users vs /users on macOS)
	rootCheck := strings.ToLower(realRoot)
	targetCheck := strings.ToLower(realTarget)
	// 6. The Security Check
	if !strings.HasPrefix(targetCheck, rootCheck) {
		return "", fmt.Errorf("path %s is outside root %s", cleanTarget, ds.allowedDir)
	}
	return cleanTarget, nil
}

// ================================================================
// Dangerous write/edit/delete operations
// ================================================================

func (ds *DirectorySandbox) isDangerousWriteOperation(cmd string) bool {
	dangerous := []string{
		"rm ", "rm -rf", "mv ", "cp ", "dd ", "truncate",
		"chmod", "chown", "tee ", "echo " + ">",
		"sed -i", "perl -pi", "> ", ">> ", "git clone",
	}
	for _, d := range dangerous {
		if strings.Contains(cmd, d) {
			return true
		}
	}
	return false
}

// ================================================================
// Escape detection
// ================================================================

func (ds *DirectorySandbox) containsDirectoryEscape(cmd string) bool {
	escapePatterns := []string{
		"../", "..\\", "cd ..", "cd ../", "cd ..\\",
	}

	for _, p := range escapePatterns {
		if strings.Contains(cmd, p) {
			return true
		}
	}
	return false
}

// ================================================================
// Extract file paths from a shell command
// ================================================================

func (ds *DirectorySandbox) extractFileArguments(cmd string) []string {
	var out []string

	cleanCmd := strings.ReplaceAll(cmd, "\"", "")
	cleanCmd = strings.ReplaceAll(cleanCmd, "'", "")

	words := strings.Fields(cleanCmd)

	for _, w := range words {
		if strings.HasPrefix(w, "-") {
			continue
		}
		if ds.isCommonNonFileArgument(w) {
			continue
		}
		if w == "|" || w == ">" || w == ">>" || w == "echo" {
			continue
		}
		out = append(out, w)
	}

	return out
}

func (ds *DirectorySandbox) isCommonNonFileArgument(arg string) bool {
	patterns := []string{
		"http://", "https://", "ftp://",
		"localhost", "127.0.0.1", "0.0.0.0",
		"yes", "no", "true", "false",
	}

	for _, p := range patterns {
		if strings.Contains(arg, p) {
			return true
		}
	}
	return false
}

// ================================================================
// Directory control
// ================================================================

func (ds *DirectorySandbox) ChangeDirectory(newDir string) error {
	if ds.mode == SandboxDisabled {
		return os.Chdir(newDir)
	}

	safePath, err := ds.ValidateSafePath(newDir)
	if err != nil {
		return fmt.Errorf("directory change outside sandbox: %s", newDir)
	}

	if err := os.Chdir(safePath); err != nil {
		return err
	}

	color.Green("📁 Changed directory: %s", safePath)
	return nil
}

// WrapCommand executes the command strictly within the sandbox logic.
// FIX: Decouples execution context from the jail boundary. Commands execute
// in the dynamic CWD, but paths are validated against the original root.
func (ds *DirectorySandbox) WrapCommand(cmd string, cfg ExecuteConfig, env shell.Env) error {
	// 1. Validate against the sandbox boundary
	if ok, reason := ds.ValidateCommand(cmd); !ok {
		return fmt.Errorf("sandbox violation: %s", reason)
	}

	if cfg.DryRun {
		color.Yellow("[Dry Run] Would execute: %s", cmd)
		return nil
	}

	// 2. Prepare Execution
	shellBin := "/bin/sh"
	if env.Shell != "" && env.Shell != "unknown" {
		shellBin = env.Shell
	}

	var c *exec.Cmd
	if strings.Contains(strings.ToLower(env.OSName), "windows") {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command(shellBin, "-c", cmd)
	}

	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	// FIX: Use the dynamic current working directory for execution context.
	// The sandbox's job is to VALIDATE paths against allowedDir, not to FORCE
	// execution in the original launch directory.
	cwd, err := os.Getwd()
	if err == nil {
		c.Dir = cwd
	} else {
		c.Dir = ds.allowedDir // Fallback to cached root if os.Getwd fails
	}

	err = c.Run()
	if err != nil {
		// FIX: Stop blindly swallowing all non-zero exit codes.
		// Only ignore exits for known informational tools (grep, diff, find).
		if exitErr, ok := err.(*exec.ExitError); ok {
			code := exitErr.ExitCode()
			if isNonFatalExit(cmd, code) {
				color.Yellow("Non-fatal exit (%d) — continuing", code)
				return nil
			}
			return fmt.Errorf("command execution failed (exit %d): %w", code, err)
		}
		return fmt.Errorf("command execution failed: %w", err)
	}
	return nil
}

func (ds *DirectorySandbox) PrintStatus() {
	color.Cyan("🔒 Sandbox Status:")
	color.Cyan("  Mode: %s", ds.ModeString())
	color.Cyan("  Allowed Directory: %s", ds.allowedDir)

	cwd, _ := os.Getwd()
	color.Cyan("  Current Working Directory: %s", cwd)
}

// ================================================================
// Helpers
// ================================================================

func (ds *DirectorySandbox) SetMode(mode SandboxMode) {
	ds.mode = mode
	color.Yellow("🔒 Sandbox mode set to: %s", ds.ModeString())
}

func (ds *DirectorySandbox) GetMode() SandboxMode {
	return ds.mode
}

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

func (ds *DirectorySandbox) ResetToOriginal() error {
	if err := os.Chdir(ds.originalDir); err != nil {
		return err
	}

	ds.allowedDir = ds.originalDir
	color.Green("📁 Reset to: %s", ds.originalDir)
	return nil
}

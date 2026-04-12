// internal/commands/sandbox.go

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"helix/internal/shell"
	"helix/internal/telemetry"

	"github.com/fatih/color"
)

// ─────────────────────────────────────────────────────────────────────────────
// THESIS TELEMETRY CONSTANTS
// ─────────────────────────────────────────────────────────────────────────────
// Intervention type constants (same as shell.go for consistency)
const (
	InterventionNone    = 0 // No intervention - command passed
	InterventionStatic  = 1 // Static regex pattern blocked command (shell.go)
	InterventionRisk    = 2 // Risk scoring flagged command (risk.go)
	InterventionSandbox = 3 // Directory sandbox blocked command
)

// SandboxMode defines the restriction level
type SandboxMode int

const (
	SandboxDisabled SandboxMode = iota
	SandboxCurrentDir
	SandboxStrict
)

// String returns the string representation of the sandbox mode
func (m SandboxMode) String() string {
	switch m {
	case SandboxDisabled:
		return "Disabled"
	case SandboxCurrentDir:
		return "CurrentDirectory"
	case SandboxStrict:
		return "Strict"
	default:
		return "Unknown"
	}
}

// DirectorySandbox with Mode C (read-only outside sandbox)
type DirectorySandbox struct {
	allowedDir  string
	mode        SandboxMode
	originalDir string
}

// NewDirectorySandbox creates default sandbox
func NewDirectorySandbox() *DirectorySandbox {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// FIX: Immediately resolve symlinks to establish the "Canonical Root"
	// This prevents issues where /var/tmp vs /private/var/tmp causes blocks.
	realCwd, err := filepath.EvalSymlinks(cwd)
	if err == nil {
		cwd = realCwd
	}

	ds := &DirectorySandbox{
		allowedDir:  cwd,
		mode:        SandboxCurrentDir,
		originalDir: cwd,
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record sandbox initialization
	// ─────────────────────────────────────────────────────────────────
	tc := telemetry.GetCollector()
	tc.Record(
		tc.GetCurrentTaskID(),
		"safety",
		"sandbox",
		"sandbox_initialized",
		true,
		map[string]interface{}{
			"mode":         ds.mode.String(),
			"allowed_dir":  ds.allowedDir,
			"original_dir": ds.originalDir,
			"cwd_resolved": cwd,
		},
	)

	return ds
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

// ValidateCommand checks if a command is allowed under sandbox rules.
// Records telemetry for command validation and sandbox violations.
func (ds *DirectorySandbox) ValidateCommand(command string) (bool, string) {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	if ds.mode == SandboxDisabled {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Sandbox disabled - all commands allowed
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"validation_completed",
			true,
			map[string]interface{}{
				"result":       "allowed",
				"reason":       "sandbox_disabled",
				"intervention": InterventionNone,
				"command_preview": func() string {
					if len(command) > 50 {
						return command[:50] + "..."
					}
					return command
				}(),
			},
		)
		return true, ""
	}

	commandLower := strings.ToLower(command)
	violations := []string{}

	// 1. Detect dangerous write/delete/edit operations
	if ds.isDangerousWriteOperation(commandLower) {
		args := ds.extractFileArguments(commandLower)

		for _, arg := range args {
			if arg == "" {
				continue
			}

			// FIX: Use the robust ValidateSafePath instead of simple string checks
			if _, err := ds.ValidateSafePath(arg); err != nil {
				violations = append(violations, fmt.Sprintf(
					"write/delete/edit operation outside sandbox: %s", arg))
			}
		}
	}

	// 2. Detect directory escape attempts (../ etc.)
	if ds.containsDirectoryEscape(commandLower) {
		violations = append(violations, "directory escape attempt detected")
	}

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Record validation outcome
	// ─────────────────────────────────────────────────────────────────
	if len(violations) > 0 {
		// Command blocked by sandbox
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"sandbox_intervention",
			false,
			map[string]interface{}{
				"result":         "blocked",
				"intervention":   InterventionSandbox,
				"violation_type": "path_traversal",
				"violations":     violations,
				"command_preview": func() string {
					if len(command) > 100 {
						return command[:100] + "..."
					}
					return command
				}(),
			},
		)
		return false, "command attempts directory escape"
	}

	// Command allowed by sandbox
	tc.Record(
		taskID,
		"safety",
		"sandbox",
		"validation_completed",
		true,
		map[string]interface{}{
			"result":       "allowed",
			"reason":       "passed_all_checks",
			"intervention": InterventionNone,
		},
	)

	return true, ""
}

// ================================================================
// ROBUST PATH VALIDATION (The Fix for your Error)
// ================================================================

// ValidateSafePath checks if a target path is inside the sandbox.
// It handles Symlinks, Case-Sensitivity (macOS/Windows), and Non-Existent files.
// Records telemetry for each path validation.
func (ds *DirectorySandbox) ValidateSafePath(targetPath string) (string, error) {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	if ds.mode == SandboxDisabled {
		return targetPath, nil
	}

	// 1. Absolutize
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(ds.allowedDir, targetPath)
	}

	// 2. Clean
	cleanTarget := filepath.Clean(targetPath)

	// 3. Resolve Symlinks (Target)
	// If the file doesn't exist (e.g. creating "danger.txt"), we resolve its PARENT.
	realTarget, err := filepath.EvalSymlinks(cleanTarget)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist; check parent directory instead
			parent := filepath.Dir(cleanTarget)
			realParent, err := filepath.EvalSymlinks(parent)
			if err == nil {
				realTarget = filepath.Join(realParent, filepath.Base(cleanTarget))
			} else {
				// If parent also fails, we stick to the clean path
				realTarget = cleanTarget
			}
		} else {
			realTarget = cleanTarget
		}
	}

	// 4. Resolve Symlinks (Root) - Ensure base comparison is accurate
	realRoot, err := filepath.EvalSymlinks(ds.allowedDir)
	if err != nil {
		realRoot = ds.allowedDir
	}

	// 5. Case-Insensitive Comparison
	// This fixes the /Users vs /users issue on macOS
	rootCheck := strings.ToLower(realRoot)
	targetCheck := strings.ToLower(realTarget)

	// 6. The Security Check
	if !strings.HasPrefix(targetCheck, rootCheck) {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Path outside sandbox - blocked
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"sandbox_intervention",
			false,
			map[string]interface{}{
				"result":         "blocked",
				"intervention":   InterventionSandbox,
				"violation_type": "path_outside_sandbox",
				"target_path":    cleanTarget,
				"allowed_root":   ds.allowedDir,
				"real_target":    realTarget,
				"real_root":      realRoot,
			},
		)
		return "", fmt.Errorf("path %s is outside root %s", cleanTarget, ds.allowedDir)
	}

	// ─────────────────────────────────────────────────────────────
	// TELEMETRY: Path validation passed
	// ─────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"sandbox",
		"path_validated",
		true,
		map[string]interface{}{
			"target_path":      cleanTarget,
			"allowed_root":     ds.allowedDir,
			"symlink_resolved": realTarget != cleanTarget,
		},
	)

	return cleanTarget, nil
}

// ================================================================
// Dangerous write/edit/delete operations
// ================================================================

func (ds *DirectorySandbox) isDangerousWriteOperation(cmd string) bool {
	dangerous := []string{
		"rm ", "rm -rf", "mv ", "cp ", "dd ", "truncate",
		"chmod", "chown", "tee ", "echo " + ">",
		"sed -i", "perl -pi", "> ", ">> ",
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

	// Basic cleaning to handle quotes roughly
	cleanCmd := strings.ReplaceAll(cmd, "\"", "")
	cleanCmd = strings.ReplaceAll(cleanCmd, "'", "")

	words := strings.Fields(cleanCmd)

	for _, w := range words {
		// skip flags
		if strings.HasPrefix(w, "-") {
			continue
		}

		// skip pure URLs or tokens
		if ds.isCommonNonFileArgument(w) {
			continue
		}

		// skip operators
		if w == "|" || w == ">" || w == ">>" || w == "echo" {
			continue
		}

		// possible file path
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

// ChangeDirectory changes the current directory within sandbox constraints.
// Records telemetry for directory changes.
func (ds *DirectorySandbox) ChangeDirectory(newDir string) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	if ds.mode == SandboxDisabled {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Directory change with sandbox disabled
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"directory_change",
			true,
			map[string]interface{}{
				"result":  "changed",
				"new_dir": newDir,
				"sandbox": "disabled",
			},
		)
		return os.Chdir(newDir)
	}

	// Use our robust validator for CD as well
	safePath, err := ds.ValidateSafePath(newDir)
	if err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Directory change blocked by sandbox
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"directory_change",
			false,
			map[string]interface{}{
				"result":       "blocked",
				"attempted":    newDir,
				"error":        err.Error(),
				"intervention": InterventionSandbox,
			},
		)
		return fmt.Errorf("directory change outside sandbox: %s", newDir)
	}

	if err := os.Chdir(safePath); err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Directory change failed (OS error)
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"directory_change",
			false,
			map[string]interface{}{
				"result":  "os_error",
				"new_dir": newDir,
				"error":   err.Error(),
			},
		)
		return err
	}

	// Update allowedDir to the new path?
	// NO. In strict sandbox, the root should usually stay the same,
	// but in "CurrentDir" mode, we might allow drilling down.
	// For "Mode C" (Root Lock), we do NOT update allowedDir (The Jail Cell),
	// we just allow moving *inside* it.

	// NOTE: If you want to restrict them to the INITIAL dir always, don't update ds.allowedDir.
	// If you want to let them drill down, but not up past original, compare against originalDir.

	color.Green("📁 Changed directory: %s", safePath)

	// ─────────────────────────────────────────────────────────────
	// TELEMETRY: Directory change successful
	// ─────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"safety",
		"sandbox",
		"directory_change",
		true,
		map[string]interface{}{
			"result":       "success",
			"new_dir":      safePath,
			"previous_dir": ds.allowedDir,
		},
	)

	return nil
}

// WrapCommand executes the command strictly within the sandbox logic.
// It wires the output to os.Stdout so the TUI Hijacker can see it.
// Records telemetry for command execution.
func (ds *DirectorySandbox) WrapCommand(cmd string, cfg ExecuteConfig, env shell.Env) error {
	tc := telemetry.GetCollector()
	taskID := tc.GetCurrentTaskID()

	// 1. Validate
	if ok, reason := ds.ValidateCommand(cmd); !ok {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Command blocked by sandbox before execution
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"execution_blocked",
			false,
			map[string]interface{}{
				"result":       "blocked",
				"reason":       reason,
				"intervention": InterventionSandbox,
				"command": func() string {
					if len(cmd) > 100 {
						return cmd[:100] + "..."
					}
					return cmd
				}(),
			},
		)
		return fmt.Errorf("sandbox violation: %s", reason)
	}

	if cfg.DryRun {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Dry run mode
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"safety",
			"sandbox",
			"dry_run",
			true,
			map[string]interface{}{
				"result":  "dry_run",
				"command": cmd,
			},
		)
		color.Yellow("[Dry Run] Would execute: %s", cmd)
		return nil
	}

	// 2. Prepare Execution
	// We use "sh -c" (or cmd /C) to handle redirects like "> file.txt" properly
	shellBin := "/bin/sh"
	if env.Shell != "" {
		shellBin = env.Shell
	}

	var c *exec.Cmd
	if strings.Contains(strings.ToLower(env.OSName), "windows") {
		c = exec.Command("cmd", "/C", cmd)
	} else {
		c = exec.Command(shellBin, "-c", cmd)
	}

	// 3. Wire I/O for TUI Capture
	// This is CRITICAL for Phase 2/3 TUI to display the output
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Dir = ds.allowedDir // Force execution in allowed dir

	// ─────────────────────────────────────────────────────────────
	// TELEMETRY: Command execution started
	// ─────────────────────────────────────────────────────────────
	tc.Record(
		taskID,
		"execution",
		"sandbox",
		"execution_started",
		true,
		map[string]interface{}{
			"command":  cmd,
			"shell":    shellBin,
			"work_dir": ds.allowedDir,
		},
	)

	// Execute and capture result
	err := c.Run()

	if err != nil {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Command execution failed
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"execution",
			"sandbox",
			"execution_completed",
			false,
			map[string]interface{}{
				"command":    cmd,
				"exit_error": err.Error(),
			},
		)
	} else {
		// ─────────────────────────────────────────────────────────────
		// TELEMETRY: Command execution succeeded
		// ─────────────────────────────────────────────────────────────
		tc.Record(
			taskID,
			"execution",
			"sandbox",
			"execution_completed",
			true,
			map[string]interface{}{
				"command": cmd,
			},
		)
	}

	return err
}

// PrintStatus prints sandbox status
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

// SetMode sets the sandbox mode and records telemetry
func (ds *DirectorySandbox) SetMode(mode SandboxMode) {
	oldMode := ds.mode
	ds.mode = mode

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Sandbox mode changed
	// ─────────────────────────────────────────────────────────────────
	tc := telemetry.GetCollector()
	tc.Record(
		tc.GetCurrentTaskID(),
		"safety",
		"sandbox",
		"mode_changed",
		true,
		map[string]interface{}{
			"old_mode": oldMode.String(),
			"new_mode": mode.String(),
		},
	)

	color.Yellow("🔒 Sandbox mode set to: %s", ds.ModeString())
}

// GetMode returns the current sandbox mode
func (ds *DirectorySandbox) GetMode() SandboxMode {
	return ds.mode
}

// ModeString returns a human-readable string for the sandbox mode
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

// ResetToOriginal resets the sandbox to its original directory
func (ds *DirectorySandbox) ResetToOriginal() error {
	// Validate against original dir logic if needed
	if err := os.Chdir(ds.originalDir); err != nil {
		return err
	}

	ds.allowedDir = ds.originalDir
	color.Green("📁 Reset to: %s", ds.originalDir)

	// ─────────────────────────────────────────────────────────────────
	// TELEMETRY: Sandbox reset to original
	// ─────────────────────────────────────────────────────────────────
	tc := telemetry.GetCollector()
	tc.Record(
		tc.GetCurrentTaskID(),
		"safety",
		"sandbox",
		"reset_to_original",
		true,
		map[string]interface{}{
			"original_dir": ds.originalDir,
		},
	)

	return nil
}

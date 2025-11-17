package commands

import (
	"fmt"
	"os"
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

// NewDirectorySandbox creates default sandbox
func NewDirectorySandbox() *DirectorySandbox {
	currentDir, err := os.Getwd()
	if err != nil {
		currentDir = "."
	}

	return &DirectorySandbox{
		allowedDir:  currentDir,
		mode:        SandboxCurrentDir,
		originalDir: currentDir,
	}
}

// ================================================================
// 🔥 MODE C CORE LOGIC
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

			if ds.isOutsideSandbox(arg) {
				return false, fmt.Sprintf(
					"write/delete/edit operation outside sandbox: %s", arg)
			}
		}
	}

	// 2. Detect directory escape attempts (../ etc.)
	if ds.containsDirectoryEscape(commandLower) {
		return false, "command attempts directory escape"
	}

	// 3. ABSOLUTE PATHS ARE ALLOWED — ONLY VALIDATE TARGETS
	// No regex-based blocking — we check resolved paths instead.
	paths := ds.extractFileArguments(commandLower)
	for _, p := range paths {
		if p == "" {
			continue
		}

		// Safe: absolute path but read-only operation
		// Only validate for write scenarios (done above)
		_ = p
	}

	return true, ""
}

// ================================================================
// 🔍 Dangerous write/edit/delete operations
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
// ⚠ Escape detection
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
// 🔍 Extract file paths from a shell command
// ================================================================

func (ds *DirectorySandbox) extractFileArguments(cmd string) []string {
	var out []string
	words := strings.Fields(cmd)

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
		if w == "|" || w == ">" || w == ">>" {
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
// 🧠 Realpath-based boundary check
// ================================================================

func (ds *DirectorySandbox) isOutsideSandbox(pathStr string) bool {
	if ds.mode == SandboxDisabled {
		return false
	}

	// Resolve symlinks and clean
	real, err := filepath.EvalSymlinks(pathStr)
	if err != nil {
		real = filepath.Clean(pathStr)
	}

	// If not absolute, resolve relative to sandbox root
	if !filepath.IsAbs(real) {
		real = filepath.Join(ds.allowedDir, real)
	}

	real = filepath.Clean(real)

	// Now compare
	rel, err := filepath.Rel(ds.allowedDir, real)
	if err != nil {
		return true
	}

	return strings.HasPrefix(rel, "..")
}

// ================================================================
// 📁 Directory control
// ================================================================

func (ds *DirectorySandbox) ChangeDirectory(newDir string) error {
	if ds.mode == SandboxDisabled {
		return os.Chdir(newDir)
	}

	// Resolve
	real, err := filepath.EvalSymlinks(newDir)
	if err != nil {
		real = filepath.Clean(newDir)
	}

	// Abs if needed
	if !filepath.IsAbs(real) {
		real = filepath.Join(ds.allowedDir, real)
	}

	real = filepath.Clean(real)

	if ds.isOutsideSandbox(real) {
		return fmt.Errorf("directory change outside sandbox: %s", newDir)
	}

	if err := os.Chdir(real); err != nil {
		return err
	}

	ds.allowedDir = real
	color.Green("📁 Changed directory: %s", real)
	return nil
}

func (ds *DirectorySandbox) WrapCommand(cmd string, cfg ExecuteConfig, env shell.Env) error {
	if ok, reason := ds.ValidateCommand(cmd); !ok {
		return fmt.Errorf("sandbox violation: %s", reason)
	}
	return ExecuteCommand(cmd, cfg, env)
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

func (ds *DirectorySandbox) GetCurrentDirectory() string {
	return ds.allowedDir
}

func (ds *DirectorySandbox) ResetToOriginal() error {
	if ds.mode != SandboxDisabled && ds.isOutsideSandbox(ds.originalDir) {
		return fmt.Errorf("cannot reset outside sandbox")
	}

	if err := os.Chdir(ds.originalDir); err != nil {
		return err
	}

	ds.allowedDir = ds.originalDir
	color.Green("📁 Reset to: %s", ds.originalDir)
	return nil
}

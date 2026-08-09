// internal/shell/shell.go

package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Env contains detected environment info with enhanced details
type Env struct {
	OSName    string // windows, linux, darwin
	Shell     string // bash, zsh, powershell, cmd, fish, unknown
	ShellPath string // Full path to shell executable
	User      string // Current username
	HomeDir   string // User home directory
}

// PackageManagerInfo represents detected package manager info
type PackageManagerInfo struct {
	Name    string // apt, brew, choco, winget, pacman, etc.
	Version string // Package manager version
	Exists  bool   // Whether package manager is available
}

// DetectEnvironment inspects OS and environment variables to determine the user's shell and OS
func DetectEnvironment() Env {
	osName := runtime.GOOS
	shell, shellPath := detectShellFromEnv()
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	// Enhanced Windows detection
	if osName == "windows" {
		// PowerShell detection
		if _, ok := os.LookupEnv("PSModulePath"); ok {
			if isPowerShellAvailable() {
				shell = "powershell"
				shellPath = "powershell.exe"
			}
		}
		// CMD detection
		if shell == "unknown" {
			if comspec := os.Getenv("ComSpec"); comspec != "" {
				shell = "cmd"
				shellPath = comspec
			}
		}
		// Git Bash detection
		if isGitBashAvailable() {
			shell = "bash"
			shellPath = "bash.exe"
		}
	}

	return Env{
		OSName:    osName,
		Shell:     shell,
		ShellPath: shellPath,
		User:      user,
		HomeDir:   homeDir,
	}
}

// DetectPackageManager detects available package managers for the current OS
func DetectPackageManager(env Env) PackageManagerInfo {
	switch env.OSName {
	case "linux":
		return detectLinuxPackageManager()
	case "darwin": // macOS
		return detectMacOSPackageManager()
	case "windows":
		return detectWindowsPackageManager()
	default:
		return PackageManagerInfo{Name: "unknown", Exists: false}
	}
}

func detectShellFromEnv() (string, string) {
	shell := os.Getenv("SHELL")

	// 1. If SHELL is set and is NOT helix, use it.
	if shell != "" && !strings.Contains(strings.ToLower(shell), "helix") {
		return resolveShellName(shell), shell
	}

	// 2. SHELL is helix (login shell) or empty (GUI launcher).
	// Check for a saved underlying shell preference.
	home, _ := os.UserHomeDir()
	if home != "" {
		prefPath := filepath.Join(home, ".helix", "shell_pref")
		if data, err := os.ReadFile(prefPath); err == nil {
			savedShell := strings.TrimSpace(string(data))
			if savedShell != "" && !strings.Contains(savedShell, "helix") {
				return resolveShellName(savedShell), savedShell
			}
		}
	}

	// 3. Fallback: Probe for common interactive shells.
	// Prefer zsh on macOS, bash on Linux.
	candidates := []string{"/bin/zsh", "/bin/bash", "/usr/bin/zsh", "/usr/bin/bash"}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			// Save this preference for next time so we never probe again.
			if home != "" {
				prefPath := filepath.Join(home, ".helix", "shell_pref")
				_ = os.MkdirAll(filepath.Dir(prefPath), 0755)
				_ = os.WriteFile(prefPath, []byte(candidate), 0644)
			}
			return resolveShellName(candidate), candidate
		}
	}

	// 4. Ultimate fallback
	return "sh", "/bin/sh"
}

// resolveShellName extracts the shell name from a path.
func resolveShellName(shellPath string) string {
	shell := strings.ToLower(shellPath)
	switch {
	case strings.Contains(shell, "bash"):
		return "bash"
	case strings.Contains(shell, "zsh"):
		return "zsh"
	case strings.Contains(shell, "fish"):
		return "fish"
	case strings.Contains(shell, "powershell"):
		return "powershell"
	case strings.Contains(shell, "cmd"):
		return "cmd"
	default:
		return "sh"
	}
}

func isPowerShellAvailable() bool {
	// Check if PowerShell is available by trying to run a simple command
	return commandExists("powershell")
}

func isGitBashAvailable() bool {
	// Check common Git Bash paths on Windows
	possiblePaths := []string{
		"C:\\Program Files\\Git\\bin\\bash.exe",
		"C:\\Program Files (x86)\\Git\\bin\\bash.exe",
		"git-bash.exe",
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return commandExists("git-bash")
}

func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func detectLinuxPackageManager() PackageManagerInfo {
	managers := []struct {
		name string
		test string
	}{
		{"apt", "apt --version"},
		{"yum", "yum --version"},
		{"dnf", "dnf --version"},
		{"pacman", "pacman --version"},
		{"zypper", "zypper --version"},
		{"snap", "snap --version"},
		{"flatpak", "flatpak --version"},
	}

	for _, mgr := range managers {
		if commandExists(mgr.name) {
			return PackageManagerInfo{Name: mgr.name, Exists: true}
		}
	}

	return PackageManagerInfo{Name: "unknown", Exists: false}
}

func detectMacOSPackageManager() PackageManagerInfo {
	// Phase 15 Fix: Check common Homebrew paths explicitly, as it might not be in PATH
	// when Helix is launched from certain contexts (e.g., GUI launchers).
	brewPaths := []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
	}
	for _, p := range brewPaths {
		if _, err := os.Stat(p); err == nil {
			return PackageManagerInfo{Name: "brew", Exists: true}
		}
	}
	if commandExists("brew") {
		return PackageManagerInfo{Name: "brew", Exists: true}
	}
	if commandExists("port") {
		return PackageManagerInfo{Name: "port", Exists: true}
	}
	return PackageManagerInfo{Name: "unknown", Exists: false}
}

func detectWindowsPackageManager() PackageManagerInfo {
	if commandExists("choco") {
		return PackageManagerInfo{Name: "choco", Exists: true}
	}
	if commandExists("winget") {
		return PackageManagerInfo{Name: "winget", Exists: true}
	}
	if commandExists("scoop") {
		return PackageManagerInfo{Name: "scoop", Exists: true}
	}
	return PackageManagerInfo{Name: "unknown", Exists: false}
}

// GetShellCommandPrefix returns the appropriate prefix for shell commands
func (e Env) GetShellCommandPrefix() string {
	switch e.Shell {
	case "powershell":
		return "powershell -Command "
	case "cmd":
		return "cmd /C "
	default:
		return ""
	}
}

// IsUnixLike returns true for Unix-like shells
func (e Env) IsUnixLike() bool {
	return e.Shell == "bash" || e.Shell == "zsh" || e.Shell == "fish"
}

// IsWindows returns true for Windows shells
func (e Env) IsWindows() bool {
	return e.Shell == "powershell" || e.Shell == "cmd"
}

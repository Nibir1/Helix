// shell.go
package main

import (
	"os"
	"os/exec"
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
	// Check SHELL environment variable
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "unknown", ""
	}

	shell = strings.ToLower(shell)
	shellName := "unknown"

	switch {
	case strings.Contains(shell, "bash"):
		shellName = "bash"
	case strings.Contains(shell, "zsh"):
		shellName = "zsh"
	case strings.Contains(shell, "fish"):
		shellName = "fish"
	case strings.Contains(shell, "powershell"):
		shellName = "powershell"
	case strings.Contains(shell, "cmd"):
		shellName = "cmd"
	}

	return shellName, shell
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

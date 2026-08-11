// internal/ollama/installer.go
// Purpose: Ollama detection, installation, and service startup.
// Hardened for real-world installs: Ollama may be in PATH, in Homebrew paths,
// inside the macOS app bundle, or already running as a background service.
package ollama

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// ProgressFunc reports long-running Ollama lifecycle progress.
// total may be zero or negative for indeterminate progress.
type ProgressFunc func(stage string, current, total int64)

// findBrewBinary locates Homebrew even when it is not in PATH.
//
// Args: none.
// Returns: path to brew or empty string.
// Complexity: O(1) filesystem checks.
func findBrewBinary() string {
	if path, err := exec.LookPath("brew"); err == nil {
		return path
	}

	for _, p := range []string{
		"/opt/homebrew/bin/brew",
		"/usr/local/bin/brew",
	} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}

	return ""
}

// findOllamaBinary locates the Ollama executable in common locations.
//
// Args: none.
// Returns: path to ollama or empty string.
// Complexity: O(1) filesystem checks.
func findOllamaBinary() string {
	if path, err := exec.LookPath("ollama"); err == nil {
		return path
	}

	candidates := []string{
		"/usr/local/bin/ollama",
		"/opt/homebrew/bin/ollama",
		"/opt/ollama/bin/ollama",
		"/Applications/Ollama.app/Contents/Resources/ollama",
	}

	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidates = append(candidates,
				filepath.Join(localAppData, "Programs", "Ollama", "ollama.exe"),
				filepath.Join(localAppData, "Ollama", "ollama.exe"),
			)
		}

		if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
			candidates = append(candidates,
				filepath.Join(programFiles, "Ollama", "ollama.exe"),
			)
		}
	}

	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}

	return ""
}

// AppInstalled reports whether the Ollama desktop app appears installed.
//
// Args: none.
// Returns: bool.
// Complexity: O(1) filesystem checks.
func AppInstalled() bool {
	if runtime.GOOS == "darwin" {
		_, err := os.Stat("/Applications/Ollama.app")
		return err == nil
	}

	return false
}

// IsInstalled reports whether Ollama is discoverable on this machine.
//
// Args: none.
// Returns: bool.
// Complexity: O(1) filesystem checks.
func IsInstalled() bool {
	return findOllamaBinary() != "" || AppInstalled()
}

// Install installs Ollama using the platform-appropriate method.
//
// Args:
//   - ctx: cancellation/timeout context.
//
// Returns: error when installation fails.
// Complexity: O(installer runtime).
func Install(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	switch runtime.GOOS {
	case "darwin":
		brew := findBrewBinary()
		if brew != "" {
			return runInstallCommand(ctx, brew, "install", "--cask", "ollama")
		}

		if AppInstalled() {
			return nil
		}

		return fmt.Errorf("homebrew was not found; install Ollama from https://ollama.com/download/mac")
	case "linux":
		return runInstallCommand(ctx, "bash", "-c", "curl -fsSL https://ollama.com/install.sh | sh")
	case "windows":
		return runInstallCommand(
			ctx,
			"winget",
			"install",
			"--id", "Ollama.Ollama",
			"-e",
			"--accept-source-agreements",
			"--accept-package-agreements",
		)
	default:
		return fmt.Errorf("unsupported OS for automatic Ollama installation: %s", runtime.GOOS)
	}
}

// EnsureRunning starts Ollama if it is not already healthy.
//
// Args:
//   - ctx: cancellation/timeout context.
//
// Returns: error when Ollama cannot be made healthy.
// Complexity: O(service startup + health polling time).
func EnsureRunning(ctx context.Context) error {
	return EnsureRunningWithProgress(ctx, nil)
}

// EnsureRunningWithProgress starts Ollama if it is not already healthy.
//
// Args:
//   - ctx: cancellation/timeout context.
//   - progress: optional progress callback.
//
// Returns: error when Ollama cannot be made healthy.
// Complexity: O(service startup + health polling time).
func EnsureRunningWithProgress(ctx context.Context, progress ProgressFunc) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
	}

	report := func(stage string) {
		if progress != nil {
			progress(stage, 0, 0)
		}
	}

	client := NewClient()

	report("CHECKING OLLAMA")
	healthCtx, healthCancel := context.WithTimeout(ctx, 3*time.Second)
	err := client.Health(healthCtx)
	healthCancel()
	if err == nil {
		return nil
	}

	binary := findOllamaBinary()

	if binary != "" {
		report("STARTING OLLAMA SERVICE")

		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}

		logDir := filepath.Join(home, ".helix")
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return fmt.Errorf("create Helix log directory: %w", err)
		}

		logPath := filepath.Join(logDir, "ollama-serve.log")
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open ollama log: %w", err)
		}

		cmd := exec.Command(binary, "serve")
		cmd.Stdout = logFile
		cmd.Stderr = logFile

		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			return fmt.Errorf("failed to start ollama serve: %w", err)
		}

		// Daemon lifecycle is independent from this setup operation.
		_ = logFile.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	} else if AppInstalled() && runtime.GOOS == "darwin" {
		report("STARTING OLLAMA APP")
		if err := runInstallCommand(ctx, "open", "-a", "Ollama"); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("ollama is not installed or not discoverable; install it and retry")
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	deadline := time.Now().Add(60 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			report("WAITING FOR OLLAMA")

			healthCtx, healthCancel := context.WithTimeout(ctx, 2*time.Second)
			err := client.Health(healthCtx)
			healthCancel()

			if err == nil {
				return nil
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("ollama service did not become healthy; check ~/.helix/ollama-serve.log")
			}
		}
	}
}

// runInstallCommand runs an installer command with inherited output.
//
// Args:
//   - ctx: cancellation context.
//   - name: executable name.
//   - args: executable arguments.
//
// Returns: error when the installer fails.
// Complexity: O(installer runtime).
func runInstallCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s installation failed: %w", name, err)
	}

	return nil
}

// internal/ollama/installer.go
// Purpose: Ollama detection, installation, and service startup.
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

// IsInstalled reports whether the ollama binary is in PATH.
func IsInstalled() bool {
	_, err := exec.LookPath("ollama")
	return err == nil
}

// Install installs Ollama using the platform-appropriate method.
func Install(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	switch runtime.GOOS {
	case "darwin":
		return runInstallCommand(ctx, "brew", "install", "ollama")

	case "linux":
		return runInstallCommand(ctx, "bash", "-c", "curl -fsSL https://ollama.com/install.sh | sh")

	case "windows":
		return runInstallCommand(ctx, "winget", "install", "--id", "Ollama.Ollama", "-e")

	default:
		return fmt.Errorf("unsupported OS for automatic Ollama installation: %s", runtime.GOOS)
	}
}

// EnsureRunning starts Ollama if it is not already healthy.
func EnsureRunning(ctx context.Context) error {
	client := NewClient()

	if err := client.Health(ctx); err == nil {
		return nil
	}

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

	cmd := exec.Command("ollama", "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return fmt.Errorf("failed to start ollama serve: %w", err)
	}

	deadline := time.Now().Add(20 * time.Second)

	for time.Now().Before(deadline) {
		if err := client.Health(ctx); err == nil {
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("ollama service did not become healthy; check %s", logPath)
}

func runInstallCommand(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s installation failed: %w", name, err)
	}

	return nil
}

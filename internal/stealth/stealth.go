// internal/stealth/stealth.go
// Purpose: Local private-history execution only.
// Inherit stdio instead of capturing it. Capturing stdout/stderr
// broke interactive TUI commands (clear, vim, top) and caused escape sequences
// to be printed via [NEURAL_NET] -> .
package stealth

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

// StealthConfig configures private execution behavior.
type StealthConfig struct {
	PrivateHistory bool
	MemoryOnly     bool
}

// DefaultStealthConfig returns safe default settings.
func DefaultStealthConfig() StealthConfig {
	return StealthConfig{
		PrivateHistory: true,
		MemoryOnly:     true,
	}
}

// StealthExecutor executes commands with local private-history settings.
type StealthExecutor struct {
	config StealthConfig
}

// NewStealthExecutor creates a private-history executor.
func NewStealthExecutor(cfg StealthConfig) *StealthExecutor {
	return &StealthExecutor{config: cfg}
}

// Execute runs a shell command.
// Non-zero exit codes are returned as output, not fatal errors.
func (s *StealthExecutor) Execute(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	env := os.Environ()
	if s.config.PrivateHistory {
		env = append(env,
			"HISTFILE=/dev/null",
			"HISTSIZE=0",
			"HISTFILESIZE=0",
		)
	}
	cmd.Env = env

	// Phase 15 Fix: Inherit stdio so interactive commands (clear, vim, top)
	// work correctly and escape sequences aren't captured and printed via [NEURAL_NET].
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("private execution failed to start: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			// Non-zero exit is not fatal for mission flow.
			if _, ok := err.(*exec.ExitError); ok {
				return "", nil
			}
			return "", fmt.Errorf("private command failed: %w", err)
		}
		return "", nil
	case <-time.After(5 * time.Minute):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("private command timed out")
	}
}

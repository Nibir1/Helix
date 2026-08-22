// Package sidecar launches and supervises the local helper processes Helix
// talks to but does not contain: llama-server, whisper-server, piper.
//
// It exists because "here is the command, go run it yourself" is where the
// setup flow kept stopping. Helix already knew the binary was missing, which
// port was free, and which model to load — everything except the last step,
// which it handed back to the user. This package is that last step.
//
// The semantics are deliberate and shared by every sidecar:
//
//   - DETACHED. A sidecar loads gigabytes of weights; tying its lifetime to the
//     shell would reload them on every restart. It outlives Helix, which makes
//     it a process the user now owns — so callers must say so and say how to
//     stop it.
//   - REAPED, not released. An unreaped child that exits becomes a zombie, and
//     signal 0 succeeds on zombies, so a pid-existence check cannot tell "still
//     loading" from "died trying". A goroutine waits on it instead; Setsid means
//     that does not tie its lifetime to Helix's.
//   - LOGGED. A detached process whose output goes nowhere is undiagnosable
//     when it fails to load a model.
package sidecar

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

// Spec describes a process to launch.
type Spec struct {
	// Name identifies the sidecar in messages and log filenames.
	Name string

	// Binary is the executable, resolved on PATH by the caller.
	Binary string

	// Args are passed verbatim.
	Args []string

	// LogPath receives stdout and stderr. Required.
	LogPath string
}

// Process is a launched sidecar.
type Process struct {
	Name    string
	PID     int
	LogPath string
	Command string

	state *procState
}

type procState struct {
	exited  atomic.Bool
	waitErr error
}

// StopHint returns the command that stops this process.
func (p Process) StopHint() string { return fmt.Sprintf("kill %d", p.PID) }

// Alive reports whether the process is still running.
func (p Process) Alive() bool {
	if p.state == nil {
		return processExists(p.PID)
	}
	return !p.state.exited.Load()
}

// ExitError returns why the process ended, once it has.
func (p Process) ExitError() error {
	if p.state == nil || !p.state.exited.Load() {
		return nil
	}
	return p.state.waitErr
}

// LogPathFor returns ~/.helix/<name>.log.
func LogPathFor(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".helix")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".log"), nil
}

// Start launches the process detached and returns once it exists.
//
// It does not wait for readiness: a process appears immediately and then spends
// seconds to minutes loading a model, or exits having failed to. Those need
// different feedback — see WaitReady.
func Start(spec Spec) (Process, error) {
	if strings.TrimSpace(spec.Binary) == "" {
		return Process{}, fmt.Errorf("%s: no binary to run", spec.Name)
	}
	if spec.LogPath == "" {
		return Process{}, fmt.Errorf("%s: a log path is required", spec.Name)
	}

	// Truncate: the log diagnoses THIS launch, and a file accumulating every
	// past attempt makes the relevant error harder to find.
	log, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return Process{}, fmt.Errorf("%s: open log: %w", spec.Name, err)
	}
	defer func() { _ = log.Close() }()

	cmd := exec.Command(spec.Binary, spec.Args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	cmd.SysProcAttr = detachedAttr()

	if err := cmd.Start(); err != nil {
		return Process{}, fmt.Errorf("%s: start: %w", spec.Name, err)
	}

	state := &procState{}
	go func() {
		state.waitErr = cmd.Wait()
		state.exited.Store(true)
	}()

	return Process{
		Name:    spec.Name,
		PID:     cmd.Process.Pid,
		LogPath: spec.LogPath,
		Command: spec.Binary + " " + strings.Join(spec.Args, " "),
		state:   state,
	}, nil
}

// WaitReady polls until probe succeeds, the process dies, or ctx expires.
//
// Stopping early on death matters: without it a sidecar that failed to load in
// two seconds still burns the caller's whole timeout before reporting anything.
func (p Process) WaitReady(ctx context.Context, probe func(context.Context) error) error {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()

	var last error
	for {
		select {
		case <-ctx.Done():
			if last != nil {
				return fmt.Errorf("%s did not become ready: %w", p.Name, last)
			}
			return fmt.Errorf("%s did not become ready in time", p.Name)

		case <-ticker.C:
			if !p.Alive() {
				if err := p.ExitError(); err != nil {
					return fmt.Errorf("%s exited during startup: %w", p.Name, err)
				}
				return fmt.Errorf("%s exited during startup", p.Name)
			}
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

// LogTail returns the last n non-blank lines of a log, for reporting a failure.
func LogTail(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// processExists reports whether a pid is in the process table.
//
// Inexact by nature: a zombie is still in the table. Process.Alive consults the
// reaper instead and is exact; this is only the fallback for a pid this program
// did not launch.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(signalZero) == nil
}

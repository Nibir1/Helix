//go:build windows

// internal/shell/tty_windows.go
// Purpose: TTY ownership + signal hardening for the native shell (Windows).
// Windows does not have SIGTTIN/SIGTTOU or Unix-style foreground process groups,
// so we strictly handle Ctrl+C (os.Interrupt) and raw mode acquisition.
package shell

import (
	"fmt"
	"os"
	"os/signal"

	"golang.org/x/term"
)

// EnsureForegroundTTY prepares stdin for raw-mode line editing on Windows.
// Returns a restore function that must be deferred by the caller.
func EnsureForegroundTTY() (func(), error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("stdin is not a terminal")
	}

	// Clean exit on Ctrl+C / external termination.
	lifeCh := make(chan os.Signal, 1)
	signal.Notify(lifeCh, os.Interrupt)

	// Raw mode.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		signal.Stop(lifeCh)
		close(lifeCh)
		return nil, fmt.Errorf("failed to set raw mode: %w", err)
	}

	go func() {
		if _, ok := <-lifeCh; ok {
			_ = term.Restore(fd, oldState)
			fmt.Fprint(os.Stderr, "\nhelix: session terminated\n")
			os.Exit(130)
		}
	}()

	restore := func() {
		_ = term.Restore(fd, oldState)
		signal.Stop(lifeCh)
		close(lifeCh)
	}
	return restore, nil
}

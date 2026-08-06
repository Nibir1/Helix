//go:build !windows

// internal/shell/tty_unix.go
// Purpose: TTY ownership + signal hardening for the native shell (Unix/macOS/Linux).
// Prevents the "silent hang" class of bugs where the kernel stops a
// background process on its first TTY read (SIGTTIN), and guarantees the
// session always exits cleanly on window close / termination signals.
package shell

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// EnsureForegroundTTY prepares stdin for raw-mode line editing:
//  1. verifies stdin is a real terminal,
//  2. converts SIGTTIN/SIGTTOU (kernel "you are in the background" stops)
//     into clean, explained exits instead of an invisible hang,
//  3. best-effort claims the terminal's foreground process group (exactly
//     what login shells do at startup),
//  4. installs SIGHUP/SIGTERM/SIGQUIT/SIGINT handlers so closing the window
//     or killing the process always restores the terminal and exits,
//  5. switches stdin into raw mode.
//
// Returns a restore function that must be deferred by the caller.
func EnsureForegroundTTY() (func(), error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("stdin is not a terminal")
	}

	// (2) Never allow the kernel to STOP us on TTY I/O.
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGTTIN, syscall.SIGTTOU)
	go func() {
		s, ok := <-stopCh
		if !ok {
			return
		}
		fmt.Fprintf(os.Stderr,
			"\nhelix: received %v — another process owns this terminal; exiting cleanly\n", s)
		os.Exit(3)
	}()

	// (3) Best-effort foreground claim (mirrors login-shell behavior).
	if pg, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err == nil {
		if pg != syscall.Getpgrp() {
			_ = syscall.Setpgid(0, 0) // become our own group leader
			signal.Ignore(syscall.SIGTTOU)
			_ = unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, syscall.Getpid())
			signal.Reset(syscall.SIGTTOU)
		}
	}

	// (4) Clean exit on window close / external termination.
	lifeCh := make(chan os.Signal, 1)
	signal.Notify(lifeCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGINT)

	// (5) Raw mode.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		signal.Stop(stopCh)
		close(stopCh)
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
		signal.Stop(stopCh)
		close(stopCh)
		signal.Stop(lifeCh)
		close(lifeCh)
	}
	return restore, nil
}

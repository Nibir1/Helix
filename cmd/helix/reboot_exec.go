// cmd/helix/reboot_exec.go
//
// Purpose: how /reboot actually replaces the running shell.
//
// The obvious implementation is syscall.Exec — same PID, same terminal, no
// second process. It does not work here, and the way it fails is worth
// recording so nobody tries it again: Go's syscall.Exec takes the runtime's
// exec lock, and in a binary with live cgo callback threads that lands on a
// thread the runtime cannot park, which aborts the process with
//
//	fatal error: notesleep not on g0
//
// Helix has exactly such threads — the audio engine is CoreAudio through cgo,
// started at boot and never shut down, because oto has no teardown to call. So
// the shell died on /reboot and never came back. Verified against the real
// binary, not reasoned about: the goroutine dump names the crash and the child
// process is a zombie.
//
// Spawn-and-exit is the alternative, and the naive form is worse than the bug.
// If the parent simply exits, the shell that launched Helix sees its foreground
// job finish and starts reading the terminal again — while the new Helix is
// also reading it. Two readers on one tty is a mess nobody can type their way
// out of. And when Helix IS the login shell, the parent exiting ends the
// session and the child is hung up on.
//
// So the parent stays, as a SUPERVISOR: it spawns the new shell, ignores the
// terminal signals so they reach the child rather than itself, waits, and exits
// with whatever the child exited with. From the launching shell's seat there is
// one job for the whole life of the session.
//
// The supervisor loop is what stops this from nesting. A supervised child that
// wants to reboot does not spawn anything — it exits with rebootExitCode, and
// the supervisor already waiting on it starts the next one. However many times
// you reboot, there are two processes.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"helix/internal/update"
)

const (
	// rebootExitCode is how a supervised shell asks for another turn.
	//
	// Deliberately not 0 or 1: those are "finished" and "failed", and a
	// supervisor that respawned on either would restart a shell the user had
	// just quit. 86 is otherwise unused by Helix — the interrupt path exits
	// 130 and the panic path 42.
	rebootExitCode = 86

	// rebootSupervisedEnv marks a shell that has a supervisor waiting on it.
	rebootSupervisedEnv = "HELIX_REBOOT_SUPERVISED"
)

// restartShell either asks the supervisor for a new shell or becomes one.
//
// It never returns: both branches end in os.Exit.
func restartShell() {
	if os.Getenv(rebootSupervisedEnv) == "1" {
		// A supervisor is already waiting on this process. Exiting IS the
		// restart request; spawning here is what would nest.
		os.Exit(rebootExitCode)
	}
	os.Exit(superviseReboot())
}

// superviseReboot runs the spawn/wait loop and returns the final exit status.
func superviseReboot() int {
	exe, err := os.Executable()
	if err != nil {
		// os.Executable rather than os.Args[0] throughout: argv[0] is whatever
		// the caller chose to put there, and as a login shell it is
		// conventionally "-helix", which names no file on disk.
		fmt.Fprintf(os.Stderr, "helix: cannot locate the Helix binary to restart: %v\n", err)
		return 1
	}

	// Terminal signals belong to the child from here on. The supervisor is not
	// interactive: if Ctrl+C killed it instead, the shell the user is actually
	// typing into would be orphaned mid-session.
	stop := ignoreTerminalSignals()
	defer stop()

	base := append(os.Environ(), rebootSupervisedEnv+"=1")
	// Only the FIRST child after an install is a candidate for rollback. A
	// later one dying is an ordinary crash, and restoring a month-old binary
	// because today's session hit a bug would be a far worse cure.
	freshInstall := rebootUpdated

	for {
		env := base
		if freshInstall {
			env = append(append([]string{}, base...), rebootUpdatedEnv+"=1")
		}

		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = env
		// No Setpgid: the child stays in this process group, so it keeps the
		// controlling terminal and job control behaves as if nothing happened.

		started := time.Now()
		err := cmd.Run()
		lived := time.Since(started)

		if err == nil {
			return 0
		}
		var exitErr *exec.ExitError
		if !asExitError(err, &exitErr) {
			// The spawn itself failed. If we had just installed something, that
			// something is the obvious suspect and the previous binary is one
			// rename away.
			if freshInstall && rollbackAfterBadUpdate(exe, err) {
				freshInstall = false
				continue
			}
			fmt.Fprintf(os.Stderr, "helix: restart failed: %v\n", err)
			return 1
		}

		code := exitErr.ExitCode()
		if code == rebootExitCode {
			freshInstall = rebootUpdatedRequested()
			continue // the child asked for another turn
		}

		// A freshly installed binary that dies quickly with a failure is the
		// failure verification cannot catch: the download was authentic and the
		// program still does not run here. Bounded by BOTH conditions — a
		// non-zero exit AND a short life — so quitting the new version normally,
		// or using it for an hour and then hitting a crash, is not mistaken for
		// a bad install.
		if freshInstall && code != 0 && lived < badUpdateWindow {
			if rollbackAfterBadUpdate(exe, fmt.Errorf("exited with status %d after %s",
				code, lived.Round(time.Millisecond))) {
				freshInstall = false
				continue
			}
		}
		return code
	}
}

// badUpdateWindow is how quickly a freshly installed Helix must fail to be
// treated as a bad install rather than as a session that ended badly.
//
// Ten seconds: long enough to cover a binary that cannot link, cannot find a
// library, or panics during startup; short enough that no real session is
// inside it.
const badUpdateWindow = 10 * time.Second

// rollbackAfterBadUpdate restores the previous binary, reporting what it did.
//
// Returns whether the caller should try again with the restored binary. The
// message goes to stderr rather than through the panel primitives on purpose:
// this runs in a supervisor whose child has just failed to start, and the
// terminal state at that moment is not something to make assumptions about.
func rollbackAfterBadUpdate(exe string, cause error) bool {
	fmt.Fprintf(os.Stderr, "\nhelix: the updated binary did not start (%v)\n", cause)
	if err := update.Rollback(exe); err != nil {
		fmt.Fprintf(os.Stderr, "helix: could not roll back: %v\n", err)
		fmt.Fprintf(os.Stderr, "helix: the previous binary is at %s%s\n", exe, update.BackupSuffix)
		return false
	}
	fmt.Fprintln(os.Stderr, "helix: rolled back to the previous version; starting it now.")
	return true
}

// rebootUpdatedRequested reports whether the child that just asked to restart
// had also installed something.
//
// The child writes the flag into the continuity record's directory rather than
// telling the supervisor directly, because there is no channel between them
// once the child is running — the supervisor only sees an exit status.
func rebootUpdatedRequested() bool {
	marker, err := updateMarkerPath()
	if err != nil {
		return false
	}
	if _, err := os.Stat(marker); err != nil {
		return false
	}
	_ = os.Remove(marker)
	return true
}

// updateMarkerPath is where a child records that it installed something before
// asking for a restart.
func updateMarkerPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "update-pending"), nil
}

// noteUpdateForSupervisor drops the marker the supervisor reads.
func noteUpdateForSupervisor() {
	if path, err := updateMarkerPath(); err == nil {
		_ = os.MkdirAll(filepath.Dir(path), 0o700)
		_ = os.WriteFile(path, []byte("1"), 0o600)
	}
}

// ignoreTerminalSignals stops the supervisor from reacting to the terminal.
//
// signal.Notify with a discarded channel rather than signal.Ignore: Ignore
// would be inherited by the child through exec, which would leave the user
// unable to Ctrl+C anything inside the restarted shell.
func ignoreTerminalSignals() func() {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, terminalSignals()...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

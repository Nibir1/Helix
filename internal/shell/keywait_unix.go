//go:build !windows

// internal/shell/keywait_unix.go
// Purpose: "is there a keystroke waiting?" — asked WITHOUT consuming it.
//
// WHY THIS EXISTS. Always-listen wake (a spoken word switching the shell into
// live mode from an idle keyboard prompt) needs one thing the REPL could not
// do: wait for either a keystroke or a wake event, whichever comes first.
// ReadLine blocks in `bufio.Reader.ReadRune` over os.Stdin, and a blocked read
// cannot be pre-empted.
//
// Three mechanisms were tried against a real PTY before this one:
//
//  1. `os.Stdin.SetReadDeadline` — "file type does not support deadline". Go
//     deliberately does not register character devices with its network
//     poller, so no deadline is available on a terminal. Measured, not assumed.
//  2. Opening /dev/tty ourselves with O_NONBLOCK — same answer, same reason.
//  3. TIOCSTI, pushing a sentinel byte into the input queue to unblock the
//     read — rejected: Linux 6.2+ gates it behind CAP_SYS_ADMIN and ships it
//     disabled, and a feature that needs to fake keyboard input to work is a
//     feature built on the wrong foundation.
//
// So the read is never pre-empted. It is never STARTED until a key is actually
// waiting. `poll(2)` answers that question directly — it is called here rather
// than left to the runtime, needs no pollable-fd registration, and does not
// consume the byte it reports. ReadLine then runs exactly as it does today and
// consumes the keystroke itself, so nothing is lost and the editor is untouched.
//
// The consequence, stated because it bounds the feature: a wake word spoken
// WHILE a line is being typed is not seen until that line is submitted. The
// window this covers is the idle prompt, which is the case that matters — "I am
// not typing, I say the word, it wakes".
package shell

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// ErrKeyWaitUnsupported reports that this platform cannot answer the question.
// Callers degrade to an ordinary blocking prompt rather than guessing.
var ErrKeyWaitUnsupported = errors.New("keystroke readiness is not available on this platform")

// KeyWaitSupported reports whether KeyReady can be used here.
func KeyWaitSupported() bool { return true }

// KeyReady reports whether stdin has input available within the timeout.
//
// Returns (true, nil) when a keystroke (or paste, or any byte) is waiting; the
// byte is left in the terminal's queue for ReadLine to consume. Returns
// (false, nil) on a clean timeout — the caller's cue to check whatever else it
// is waiting for and come back.
//
// EINTR is retried rather than reported: a window resize (SIGWINCH) arrives as
// an interrupted poll, and treating that as "no key" would be true but would
// also throw away the remaining wait, which on a resize-happy terminal turns a
// 50 ms poll into a busy loop.
func KeyReady(timeout time.Duration) (bool, error) {
	fd := int(os.Stdin.Fd())
	deadline := time.Now().Add(timeout)

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false, nil
		}
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, int(remaining.Milliseconds()))
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return false, err
		}
		if n == 0 {
			return false, nil
		}
		// POLLHUP/POLLERR also mean "read will not block" — it will return EOF
		// or an error, and ReadLine already handles both (Ctrl+D exits, a read
		// error returns). Reporting ready here is what stops a closed stdin
		// from spinning this loop forever.
		return true, nil
	}
}

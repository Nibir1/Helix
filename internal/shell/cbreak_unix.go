//go:build darwin || linux

// internal/shell/cbreak_unix.go
// Purpose: hold the terminal in CBREAK mode for the duration of an operation,
// so a keystroke can be noticed while something else is happening — without
// being echoed, and without breaking Ctrl+C.
//
// WHY NOT term.MakeRaw. Raw mode clears ISIG, which turns Ctrl+C into the byte
// 0x03 instead of a signal. During a voice capture the terminal is currently
// COOKED, and Ctrl+C reaching the process as SIGINT is what cancels the
// recorder (utils.RegisterOperation → cancelAllOperations). Holding raw mode
// across a capture would silently delete a documented escape hatch — "Ctrl+C
// takes a turn now" — and nothing in the test suite could catch it, because
// catching it needs a microphone and a signal. So this is hand-rolled termios
// that clears exactly what has to be cleared and keeps ISIG.
//
// WHY THE TERMINAL HAS TO BE TOUCHED AT ALL. Two reasons, both measurable:
//
//  1. In canonical mode the line discipline buffers input until Enter, so
//     poll(2) reports nothing readable however many characters have been
//     typed. The keystroke would only be noticed once a whole line was
//     submitted — which is the same reason ArmedWait takes the terminal out of
//     canonical mode for its wait.
//  2. With ECHO on, the kernel prints each character wherever the cursor
//     happens to be — which, during a voice turn, is the middle of the
//     animated HUD line. That is the garbage a user sees today if they type
//     while Helix is listening.
//
// WHAT IS NOT DONE HERE. The pending byte is never consumed. It stays in the
// terminal's input queue across the restore and across ReadLine's own
// MakeRaw, and ReadLine reads it as if it had been typed a moment later. That
// property is the foundation the whole feature stands on (see
// keywait_unix.go), so DiscardPendingInput exists but has exactly one correct
// caller: the degraded path where nothing was watching for keystrokes and the
// bytes are genuinely stale.
package shell

import (
	"os"

	"golang.org/x/sys/unix"
)

// MakeCbreak puts the terminal into cbreak mode and returns a restorer.
//
// Cbreak is raw-minus-the-signals: no line buffering, no echo, but Ctrl+C,
// Ctrl+Z and flow control still behave. The returned function is safe to call
// more than once and must be called before anything else reads a line —
// ReadLine restores the terminal to whatever it FINDS, so leaving cbreak in
// place would make it restore into cbreak and leave the shell without echo,
// which is indistinguishable from a hang.
func MakeCbreak(f *os.File) (restore func(), err error) {
	fd := int(f.Fd())
	before, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		return nil, err
	}

	after := *before
	// Line discipline off, echo off. ISIG and IXON are deliberately UNTOUCHED.
	after.Lflag &^= unix.ICANON | unix.ECHO | unix.ECHOE | unix.ECHOK | unix.ECHONL
	// One byte is enough and never wait: this terminal is polled, not read, so
	// these only matter if something does read it.
	after.Cc[unix.VMIN] = 1
	after.Cc[unix.VTIME] = 0

	if err := unix.IoctlSetTermios(fd, ioctlWriteTermios, &after); err != nil {
		return nil, err
	}

	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		_ = unix.IoctlSetTermios(fd, ioctlWriteTermios, before)
	}, nil
}

// KeyPending reports whether a byte is already waiting, without consuming it
// and without blocking.
//
// Not KeyReady(0): that returns false WITHOUT POLLING, because its loop
// computes the remaining time first and bails when it is not positive. So the
// obvious spelling of this question silently always answers no. The trap is
// pinned by a test rather than only described here.
func KeyPending() (bool, error) {
	fds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	if err != nil {
		if err == unix.EINTR {
			return false, nil
		}
		return false, err
	}
	return n > 0, nil
}

// DiscardPendingInput throws away whatever is sitting in the input queue.
//
// ONE correct caller: the path where AWAKE could not watch the keyboard (no
// cbreak, no watcher) and bytes typed during a capture were never going to be
// acted on. Anywhere else this eats the user's first keystroke — the exact
// byte the design depends on reaching ReadLine — so a general "flush after a
// turn" would break the feature it looks like it is helping.
//
// Drained by reading rather than by a flush ioctl, because the flush ioctl is
// not portable: Linux spells it TCFLSH/TCIFLUSH and the BSDs spell it
// TIOCFLUSH/FREAD, and x/sys/unix does not export a common name. Each read is
// gated on poll, so this never blocks on an empty queue. Bounded because an
// unbounded drain against a terminal that is being typed into fast enough
// would not terminate.
func DiscardPendingInput() error {
	fd := int(os.Stdin.Fd())
	buf := make([]byte, 256)
	for i := 0; i < 64; i++ {
		pending, err := KeyPending()
		if err != nil {
			return err
		}
		if !pending {
			return nil
		}
		if _, err := unix.Read(fd, buf); err != nil {
			if err == unix.EINTR {
				continue
			}
			return err
		}
	}
	return nil
}

// CbreakSupported reports whether this platform can hold cbreak mode.
func CbreakSupported() bool { return true }

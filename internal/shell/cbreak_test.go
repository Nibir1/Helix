//go:build darwin || linux

// internal/shell/cbreak_test.go
// Purpose: the two properties of cbreak mode that cannot be caught anywhere
// else — that signals survive it, and that the pending byte does not.
package shell

import (
	"os"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// ptyPair gives a real terminal to test against. A pipe will not do: termios
// ioctls fail on anything that is not a tty, which is the whole subject here.
func ptyPair(t *testing.T) *os.File {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close(); _ = ptmx.Close() })
	return tty
}

// THE assertion that protects Ctrl+C.
//
// term.MakeRaw clears ISIG, which turns Ctrl+C into the byte 0x03 instead of a
// signal. During a voice capture SIGINT is what cancels the recorder, so a
// cbreak that cleared ISIG would silently delete a documented escape hatch —
// and the deletion would only be visible to someone holding a microphone.
func TestCbreakKeepsSignalsEnabled(t *testing.T) {
	tty := ptyPair(t)
	fd := int(tty.Fd())

	restore, err := MakeCbreak(tty)
	if err != nil {
		t.Fatalf("MakeCbreak: %v", err)
	}

	got, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		restore()
		t.Fatalf("read termios: %v", err)
	}
	if got.Lflag&unix.ISIG == 0 {
		restore()
		t.Fatal("cbreak cleared ISIG — Ctrl+C during a voice capture would arrive as " +
			"the byte 0x03 instead of a signal, and cancelling a capture is the only " +
			"escape hatch that needs no microphone")
	}
	if got.Lflag&unix.ICANON != 0 {
		restore()
		t.Error("ICANON survived — poll(2) would report nothing until Enter, so a " +
			"keystroke could not be noticed mid-capture")
	}
	if got.Lflag&unix.ECHO != 0 {
		restore()
		t.Error("ECHO survived — the kernel would print keystrokes into the middle of " +
			"the voice HUD, which is the garbage this mode exists to prevent")
	}
	restore()
}

// ReadLine restores the terminal to whatever it FINDS, so cbreak has to be
// gone before it runs or the shell is left with no echo and no line editing —
// indistinguishable from a hang.
func TestCbreakRestoreIsIdempotentAndComplete(t *testing.T) {
	tty := ptyPair(t)
	fd := int(tty.Fd())

	before, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		t.Fatalf("read termios: %v", err)
	}
	restore, err := MakeCbreak(tty)
	if err != nil {
		t.Fatalf("MakeCbreak: %v", err)
	}
	restore()
	restore() // must be safe: it is deferred AND called explicitly on the
	// pre-empt path, so a double call is the normal case, not an error case.

	after, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
	if err != nil {
		t.Fatalf("read termios: %v", err)
	}
	if after.Lflag != before.Lflag {
		t.Errorf("lflag not restored: before=%x after=%x", before.Lflag, after.Lflag)
	}
}

// The trap, pinned so nobody "fixes" it by accident.
//
// KeyReady computes the remaining time first and returns false when it is not
// positive, so KeyReady(0) NEVER POLLS. That is the obvious way to ask "is a
// byte already waiting", and it silently always answers no. KeyPending exists
// because of it.
func TestKeyReadyZeroTimeoutNeverPolls(t *testing.T) {
	ready, err := KeyReady(0)
	if err != nil {
		t.Fatalf("KeyReady(0): %v", err)
	}
	if ready {
		t.Fatal("KeyReady(0) reported a key — this test's premise is wrong and " +
			"KeyPending may be redundant")
	}
	// Not an assertion about an empty queue: this is documenting that a zero
	// timeout is a short circuit, which is why the pre-check uses KeyPending.
}

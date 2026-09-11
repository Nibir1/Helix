//go:build darwin

package shell

import "golang.org/x/sys/unix"

// The termios ioctls differ by platform and x/term keeps its equivalents
// private, so they are named here per OS — the same split x/term uses
// internally. Only these two constants vary; cbreak_unix.go is shared.
const (
	ioctlReadTermios  = unix.TIOCGETA
	ioctlWriteTermios = unix.TIOCSETA
)

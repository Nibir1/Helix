//go:build windows

// internal/shell/keywait_windows.go
// Purpose: the Windows half of keystroke readiness — honestly absent.
//
// `poll(2)` has no Windows equivalent for a console handle. The real answer
// here is `WaitForSingleObject` on the console input handle plus
// `PeekConsoleInput` to distinguish a key event from the mouse and focus
// records the console also queues — which is a genuine piece of work, not a
// one-liner, and it needs a Windows machine to test on. Guessing at it would
// produce exactly the class of bug this repo has already paid for twice on
// Windows: code that compiles, looks plausible, and fails on the one platform
// nobody ran it on.
//
// So it reports unsupported, the caller degrades to an ordinary blocking
// prompt, and `/blackbox wake always on` says why rather than silently doing
// nothing. Recorded as open work rather than pretended away.
package shell

import (
	"errors"
	"time"
)

// ErrKeyWaitUnsupported reports that this platform cannot answer the question.
var ErrKeyWaitUnsupported = errors.New(
	"always-listen wake needs keystroke readiness, which is not implemented on Windows yet")

// KeyWaitSupported reports whether KeyReady can be used here.
func KeyWaitSupported() bool { return false }

// KeyReady always reports unsupported on Windows.
func KeyReady(time.Duration) (bool, error) { return false, ErrKeyWaitUnsupported }

//go:build windows

// internal/shell/cbreak_windows.go
// Purpose: the honest absence, matching keywait_windows.go.
//
// Windows has no termios, and the console API's equivalent (disabling
// ENABLE_LINE_INPUT and ENABLE_ECHO_INPUT while keeping
// ENABLE_PROCESSED_INPUT) is a different mechanism that has never been
// measured against this shell's line editor. Rather than ship an untested
// approximation, AWAKE takes voice-only turns here and /blackbox status says
// so — the same posture KeyWaitSupported already takes for the armed prompt.
package shell

import "os"

// MakeCbreak is unavailable on Windows.
func MakeCbreak(*os.File) (func(), error) { return nil, ErrKeyWaitUnsupported }

// KeyPending is unavailable on Windows.
func KeyPending() (bool, error) { return false, ErrKeyWaitUnsupported }

// DiscardPendingInput is unavailable on Windows.
func DiscardPendingInput() error { return ErrKeyWaitUnsupported }

// CbreakSupported reports whether this platform can hold cbreak mode.
func CbreakSupported() bool { return false }

//go:build windows

package speech

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isPlatformConnRefused matches the errno a refused TCP connect actually
// carries on Windows, which syscall.ECONNREFUSED does not equal there.
func isPlatformConnRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}

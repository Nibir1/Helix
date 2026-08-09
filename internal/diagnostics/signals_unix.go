//go:build !windows

// internal/diagnostics/signals_unix.go
// Purpose: Unix fatal signal set (SIGBUS/SIGILL/SIGFPE exist on Unix only).
package diagnostics

import (
	"os"
	"syscall"
)

// fatalSignals returns the signals that indicate a fatal process fault.
func fatalSignals() []os.Signal {
	return []os.Signal{
		syscall.SIGSEGV, syscall.SIGABRT, syscall.SIGBUS, syscall.SIGILL, syscall.SIGFPE,
	}
}

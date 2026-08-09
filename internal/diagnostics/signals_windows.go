//go:build windows

// internal/diagnostics/signals_windows.go
// Purpose: Windows fatal signal set (C-runtime signals only).
package diagnostics

import (
	"os"
	"syscall"
)

// fatalSignals returns the signals that indicate a fatal process fault.
func fatalSignals() []os.Signal {
	return []os.Signal{syscall.SIGSEGV, syscall.SIGABRT}
}

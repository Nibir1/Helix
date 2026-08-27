//go:build windows

package main

import (
	"os"
	"syscall"
)

// terminalSignals is the Windows equivalent: a console Ctrl+C arrives as
// SIGINT, and the job-control signals have no counterpart.
func terminalSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT}
}

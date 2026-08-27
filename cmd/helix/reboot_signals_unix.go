//go:build !windows

package main

import (
	"os"
	"syscall"
)

// terminalSignals are the ones a terminal delivers to the whole foreground
// process group, which the supervisor must let pass to the child.
//
// SIGHUP is absent on purpose: the terminal really is going away, and a
// supervisor that survived it would keep a shell alive with nothing attached.
func terminalSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP}
}

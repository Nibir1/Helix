//go:build !windows

package llamacpp

import (
	"os"
	"syscall"
)

// detachedAttr puts llama-server in its own session with no controlling
// terminal, so it neither contends with Helix's raw-mode line reader nor dies
// when the shell exits.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// syscallZero is signal 0: delivered to nothing, but errors if the process is
// gone — the portable existence check.
const syscallZero = syscall.Signal(0)

// processExists reports whether a pid is in the process table.
//
// Signal 0 is delivered to nothing but errors when the process is gone.
func processExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscallZero) == nil
}

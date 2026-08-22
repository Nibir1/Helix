//go:build !windows

package llamacpp

import "syscall"

// detachedAttr puts llama-server in its own session with no controlling
// terminal, so it neither contends with Helix's raw-mode line reader nor dies
// when the shell exits.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// syscallZero is signal 0: delivered to nothing, but errors if the process is
// gone — the portable existence check.
const syscallZero = syscall.Signal(0)

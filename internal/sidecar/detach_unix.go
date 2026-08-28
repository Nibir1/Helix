//go:build !windows

package sidecar

import "syscall"

// detachedAttr puts the sidecar in its own session with no controlling
// terminal, so it neither contends with Helix's raw-mode line reader nor dies
// with the shell.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// signalZero is delivered to nothing but errors when the process is gone.
const signalZero = syscall.Signal(0)

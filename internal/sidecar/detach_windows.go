//go:build windows

package sidecar

import "syscall"

// detachedAttr requests a new process group. Windows has no setsid; this is the
// nearest equivalent and keeps a Ctrl+C in Helix from killing the sidecar.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}

// signalZero is the existence check.
const signalZero = syscall.Signal(0)

//go:build windows

package llamacpp

import "syscall"

// detachedAttr requests a process without a console window. Windows has no
// setsid; CREATE_NEW_PROCESS_GROUP is the nearest equivalent and is what keeps
// a Ctrl+C in Helix from also killing the server.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
}

// syscallZero is signal 0, the existence check.
const syscallZero = syscall.Signal(0)

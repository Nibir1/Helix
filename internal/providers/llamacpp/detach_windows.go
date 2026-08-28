//go:build windows

package llamacpp

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// detachedAttr requests a process without a console window. Windows has no
// setsid; CREATE_NEW_PROCESS_GROUP is the nearest equivalent and is what keeps
// a Ctrl+C in Helix from also killing the server.
func detachedAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// processExists reports whether a pid is in the process table.
//
// Signal 0 is a Unix idiom and there is no Windows equivalent:
// os.Process.Signal refuses everything but Kill here, so asking that way
// reported every process — including this one — as dead.
//
// The handle is opened for query only, and the exit code is what decides.
// os.FindProcess alone is not enough: it succeeds for a process that has
// already exited but whose handle is still open, which is exactly the case
// this check exists to catch.
func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

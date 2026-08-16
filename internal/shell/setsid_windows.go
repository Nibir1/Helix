// internal/shell/setsid_windows.go
// Purpose: Windows counterpart of setsid_unix.go. The login-env probe never
// runs on Windows (the registry-built environment is already complete), so
// no detachment is required.
//go:build windows

package shell

import "syscall"

// sysProcAttrDetached returns nil on Windows: there is no controlling
// terminal to detach from and no probe process to isolate.
func sysProcAttrDetached() *syscall.SysProcAttr {
	return nil
}

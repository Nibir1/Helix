// internal/shell/setsid_unix.go
// Purpose: detach the login-env probe shell from Helix's controlling
// terminal so it can never race the raw-mode line reader for termios or
// process-group state. Unix-only; Windows has no Setsid concept.
//go:build !windows

package shell

import "syscall"

// sysProcAttrDetached returns SysProcAttr that puts the child in a new
// session with no controlling terminal.
func sysProcAttrDetached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

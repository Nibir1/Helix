//go:build linux

package shell

import "golang.org/x/sys/unix"

// See cbreak_ioctl_darwin.go for why these are per-OS constants.
const (
	ioctlReadTermios  = unix.TCGETS
	ioctlWriteTermios = unix.TCSETS
)

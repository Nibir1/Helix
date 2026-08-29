//go:build !windows

package main

import "golang.org/x/sys/unix"

// freeBytes reports the space available to this user on the filesystem holding
// path, or false if it cannot be determined.
//
// Bavail rather than Bfree: the difference is the reserve only root may use,
// and reporting space an ordinary process cannot actually have is the same as
// reporting the wrong number.
func freeBytes(path string) (uint64, bool) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true //nolint:gosec,unconvert // widths differ per platform
}

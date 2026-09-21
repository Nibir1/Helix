//go:build !windows

package uninstall

// RunningBinary is always false on Unix: deleting a running executable is
// legal, because the inode outlives the directory entry. That is what lets
// /purge remove the shell it is executing inside and still print its report.
func RunningBinary(string) bool { return false }

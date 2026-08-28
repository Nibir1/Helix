//go:build !windows

package speech

// isPlatformConnRefused has nothing to add outside Windows: syscall.ECONNREFUSED
// is the errno a refused connect carries here.
func isPlatformConnRefused(error) bool { return false }

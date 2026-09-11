// internal/uninstall/uninstall_windows_test.go
// Purpose: the Windows counterpart of the shells quarantine — there is no
// /etc/shells to protect, so there is nothing to point away from.
package uninstall

// quarantineSystemPaths is a no-op on Windows: planShell returns nothing, so no
// test can reach a system file through this package.
func quarantineSystemPaths() {}

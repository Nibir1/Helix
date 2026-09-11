// internal/uninstall/uninstall_windows.go
// Purpose: the Windows half, where three of the Unix concepts do not exist.
//
// There is no /etc/shells, no chsh and no login shell to restore — Helix is
// added to a terminal emulator's profile by hand (install.sh says so), and
// nothing on disk records which one. So `helix uninstall` cannot undo that, and
// says so rather than pretending.
//
// The other difference is real and not cosmetic: **Windows cannot delete a
// running executable.** On Unix the inode outlives the directory entry, so
// /purge can remove the shell it is running inside. Here the removal fails with
// a sharing violation, so the binary is reported as the one thing left to do
// by hand, with the exact command.
//
// HONESTLY LABELLED: typechecked by `GOOS=windows go vet` and never run —
// there is no Windows machine here. What is verified is that it compiles and
// that its failure is a clear instruction rather than a silent no-op.
package uninstall

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// planShell has nothing to do on Windows: there is no login shell to restore
// and no /etc/shells to edit.
func planShell(string) []Item { return nil }

// planServices returns nothing: `helix daemon install` prints instructions on
// Windows rather than writing a unit, so there is no file to remove.
func planServices(string) []Item { return nil }

// removeShellItem is unreachable — planShell returns no items.
func removeShellItem(Item) error {
	return errors.New("no login shell registration exists on Windows")
}

// needsRoot has no meaningful answer here. Windows uses ACLs rather than a
// single root bit, and reporting "needs root" would send the user looking for
// a sudo that does not exist. A permission failure surfaces at removal with
// the real error instead.
func needsRoot(string) bool { return false }

// RunningBinary reports whether path is the executable of this process, which
// on Windows means it cannot be deleted from inside it.
func RunningBinary(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	a, _ := filepath.EvalSymlinks(exe)
	b, _ := filepath.EvalSymlinks(path)
	return a != "" && a == b
}

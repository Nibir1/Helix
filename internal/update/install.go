// internal/update/install.go
//
// Purpose: putting a verified binary in place, reversibly.
//
// The failure this file exists to survive is a good update that turns out to be
// a bad binary. Verification proves the download matches the release; it cannot
// prove the release runs on this machine. So the previous binary is kept, and
// the supervisor restores it when the replacement cannot start.
package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// BackupSuffix names the retained previous binary.
//
// Beside the target rather than in a temp directory, because the rollback has
// to work when /tmp has been cleared, the disk is nearly full, or the process
// doing the rollback is a supervisor that knows nothing but its own path.
const BackupSuffix = ".prev"

// Install replaces target with newBinary, keeping the old one.
//
// Returns the backup path so a caller can roll back or clean up.
//
// The steps are ordered so that at no point does `target` name a file that is
// neither the old binary nor the new one:
//
//  1. copy the new binary alongside the target, as a temp file on the SAME
//     filesystem — rename is only atomic within one filesystem, and /tmp is
//     routinely a different one;
//  2. hard-link or copy the current target to <target>.prev;
//  3. rename the temp over the target, which is atomic;
//  4. remove the temp on any failure before step 3.
func Install(newBinary, target string) (backup string, err error) {
	target, err = filepath.Abs(target)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(target)

	staged := filepath.Join(dir, ".helix-update-staged")
	if err := copyFile(newBinary, staged, 0o755); err != nil {
		return "", fmt.Errorf("stage the new binary: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.Remove(staged)
		}
	}()

	if err := os.Chmod(staged, 0o755); err != nil {
		return "", fmt.Errorf("make the new binary executable: %w", err)
	}

	backup = target + BackupSuffix
	_ = os.Remove(backup)
	if err := copyFile(target, backup, 0o755); err != nil {
		// A missing target is not a reason to stop — an install into a path
		// that does not exist yet is a legitimate first install — but a
		// target that exists and cannot be backed up is, because it would
		// leave nothing to roll back to.
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("back up the current binary: %w", err)
		}
		backup = ""
	}

	if err := os.Rename(staged, target); err != nil {
		return backup, fmt.Errorf("install the new binary: %w", err)
	}
	return backup, nil
}

// Rollback restores a backup over the target.
//
// Best-effort by design and by necessity: it runs when something has already
// gone wrong, and a rollback that itself refuses to proceed leaves the user with
// the broken binary and a second error message.
func Rollback(target string) error {
	backup := target + BackupSuffix
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("no backup to roll back to")
	}
	staged := target + ".rollback"
	if err := copyFile(backup, staged, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

// WritableTarget reports whether this process can replace the binary in place.
//
// Checked BEFORE downloading anything. A Helix installed under /usr/local/bin
// by a package manager is not writable by the user running it, and discovering
// that after a 40 MB download — with a staged file already beside the target —
// is a worse experience than a clear refusal with the reason attached.
func WritableTarget(target string) error {
	dir := filepath.Dir(target)
	probe, err := os.CreateTemp(dir, ".helix-write-probe-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)

	// Windows refuses to rename over a running executable, so an in-place
	// replacement of the file this process is executing cannot work there. The
	// supervisor makes it work anyway — the child exits before the parent
	// installs — and this check exists for the directory permission, which is
	// the part that differs per machine.
	if runtime.GOOS == "windows" {
		return nil
	}
	return nil
}

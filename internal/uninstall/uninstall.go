// internal/uninstall/uninstall.go
// Purpose: enumerate and remove EVERY trace of Helix from this machine.
//
// WHY THIS IS A PACKAGE AND NOT A SHELL SCRIPT. `make install` is a script
// because it runs before there is a binary; uninstall runs when there is one,
// and it has three callers — `make uninstall`, `helix uninstall`, and /purge's
// third confirmation. Three copies of a destructive path is the drift shape
// this repo has paid for repeatedly (see the Endpoints-dropped-at-the-boundary
// entries in §13), and here a drifted copy does not misreport a port, it leaves
// a launchd job pointing at a deleted binary.
//
// THE ORDER IS A SAFETY PROPERTY, not tidiness. The login shell comes first and
// the binary comes last:
//
//  1. restore the login shell   — a login shell that does not exist means no
//     new terminal will start. This MUST succeed
//     before the binary goes, or the user is locked
//     out of their own machine.
//  2. stop and remove services  — a launchd job pointing at a deleted binary is
//     retried forever and logs on every attempt.
//  3. /etc/shells               — cosmetic on its own, but it is what `chsh`
//     validates against.
//  4. data (~/.helix, history)  — losing this while the binary survives is
//     recoverable; the reverse is not.
//  5. the binary                — last, because everything above may need it,
//     and because it is the step that needs root.
//
// Deleting the RUNNING binary is fine on Unix: the inode outlives the directory
// entry, so /purge can remove the shell it is executing inside and finish its
// own output. Windows cannot, which is why removeBinary is platform-split.
package uninstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind groups an item by what losing it costs, which is what the manifest
// orders by. Same principle as purge.go's groups: a credential you must fetch
// from a vendor dashboard is not a cache.
type Kind int

const (
	// KindShell is the login-shell registration. Removed FIRST and separately,
	// because it is the only item that can lock a user out.
	KindShell Kind = iota
	// KindService is an OS service unit (launchd plist, systemd unit).
	KindService
	// KindData is everything under the Helix home plus the history file.
	KindData
	// KindBinary is the installed executable.
	KindBinary
)

func (k Kind) String() string {
	switch k {
	case KindShell:
		return "shell registration"
	case KindService:
		return "background service"
	case KindData:
		return "data and credentials"
	default:
		return "the binary"
	}
}

// Item is one thing to remove.
type Item struct {
	// Path is what will be deleted, or the file that will be edited for
	// KindShell.
	Path string

	// Desc says what it is, in the user's terms.
	Desc string

	Kind Kind

	// NeedsRoot is true when the current user cannot remove it. Computed by
	// probing the PARENT directory rather than the file: removal is a directory
	// write, and a root-owned file in a user-writable directory is removable
	// while a user-owned file in /usr/local/bin is not.
	NeedsRoot bool

	// Edit marks an item that is modified rather than deleted — /etc/shells
	// loses one line and keeps the rest. Deleting it would break every other
	// shell on the machine.
	Edit bool

	// EditLine is the exact line an Edit item removes. Separate from Desc so
	// the manifest can say what is happening in words while the removal matches
	// byte for byte.
	EditLine string
}

// Plan enumerates every Helix artifact that EXISTS on this machine.
//
// Absent things are omitted rather than listed as "not found": the manifest is
// read immediately before an irreversible yes, and padding it with rows that do
// nothing is how the one row that matters gets skimmed past (purge.go's own
// lesson, and the reason its weights prompt only appears when weights exist).
func Plan(home, exePath string) []Item {
	var items []Item
	add := func(path, desc string, kind Kind) {
		if path == "" || !exists(path) {
			return
		}
		items = append(items, Item{
			Path: path, Desc: desc, Kind: kind, NeedsRoot: needsRoot(path),
		})
	}

	// 1. The login shell, and the file chsh validates against.
	items = append(items, planShell(home)...)

	// 2. Services.
	for _, s := range planServices(home) {
		add(s.Path, s.Desc, KindService)
	}

	// 3. Data. The Helix home goes WHOLESALE rather than file by file.
	//
	// /purge enumerates, and that is right for /purge: it names what it takes
	// so the user can read it. An uninstall must not enumerate, because the
	// enumeration is already incomplete — `models.json`, `shell_pref` and any
	// key file a user dropped in by hand are all in ~/.helix today and none of
	// them is in purge.go's list. "Everything related to Helix" cannot be
	// spelled as a list that someone has to remember to extend.
	add(filepath.Join(home, ".helix"), "the Helix home — keys, config, databases, models, logs", KindData)
	add(filepath.Join(home, ".helix_history"), "command history", KindData)

	// 4. The binary, and anything the self-updater left beside it.
	if exePath != "" {
		add(exePath, "the installed binary", KindBinary)
		add(exePath+".prev", "the previous binary, kept by /reboot for rollback", KindBinary)
		add(exePath+".rollback", "an interrupted rollback", KindBinary)
		add(filepath.Join(filepath.Dir(exePath), ".helix-update-staged"), "an interrupted update", KindBinary)
	}
	return items
}

// NeedsRootAny reports whether any item in the plan requires elevation, so a
// caller can ask for a password once and up front rather than failing halfway.
func NeedsRootAny(items []Item) bool {
	for _, it := range items {
		if it.NeedsRoot {
			return true
		}
	}
	return false
}

// Apply removes the items, in the order Plan produced them, and returns what
// failed.
//
// Best-effort past a failure ON PURPOSE, with one exception. A partial
// uninstall that stops at the first error leaves the user with a machine in an
// unknown state and no way to finish; carrying on and reporting every failure
// at the end leaves them with a short, actionable list. The exception is the
// login shell: if that cannot be restored, the binary is NOT removed, because
// the two failures compose into a machine that cannot open a terminal.
func Apply(items []Item) (removed int, failures []string) {
	shellOK := true
	for _, it := range items {
		if it.Kind == KindBinary && !shellOK {
			failures = append(failures, fmt.Sprintf(
				"%s: kept deliberately — the login shell still points at Helix, and removing "+
					"it now would leave you unable to open a terminal", it.Path))
			continue
		}
		if err := remove(it); err != nil {
			if it.Kind == KindShell {
				shellOK = false
			}
			failures = append(failures, fmt.Sprintf("%s: %v", it.Path, err))
			continue
		}
		removed++
	}
	return removed, failures
}

// remove performs one item's removal.
func remove(it Item) error {
	switch {
	case it.Kind == KindShell:
		return removeShellItem(it)
	case it.Edit:
		return dropLine(it.Path, it.EditLine)
	default:
		return os.RemoveAll(it.Path)
	}
}

// dropLine removes every line equal to `line` from a file, keeping the rest.
//
// Rewrites through a temp file in the same directory and renames, so a crash
// midway cannot leave /etc/shells truncated — a zero-length /etc/shells is a
// machine where chsh refuses every shell.
func dropLine(path, line string) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- a fixed system path
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	var kept []string
	dropped := false
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == line {
			dropped = true
			continue
		}
		kept = append(kept, l)
	}
	if !dropped {
		return nil
	}
	tmp := path + ".helix-uninstall"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")), info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

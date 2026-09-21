//go:build !windows

// internal/uninstall/uninstall_unix.go
// Purpose: the parts of an uninstall that only exist on a Unix-like machine —
// the login shell, /etc/shells, launchd and systemd.
package uninstall

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// etcShells is the file `chsh` validates a shell against.
//
// A VAR RATHER THAN A CONST so the suite can point it at a temp file, and that
// is not a convenience — the first version was a const, and the tests promptly
// showed Plan reading the real /etc/shells whatever home it was handed. On this
// machine that file contains a Helix line, so the "empty machine" case returned
// one item, and TestTheHelixHomeIsRemovedWholesale tried to REWRITE the real
// /etc/shells. It was stopped by not being root, which is luck rather than
// design. Nothing outside a test assigns this.
var etcShells = "/etc/shells"

// planShell returns the login-shell work, which is TWO different things that
// are easy to conflate:
//
//   - the user's login shell actually being Helix, which is the lockout risk;
//   - the /etc/shells registration, which is just a line in a file.
//
// Only the first can hurt, and it is only present on machines where `chsh` was
// actually run — the installer asks, and most people say no.
func planShell(home string) []Item {
	var items []Item
	if current := loginShell(); current != "" && isHelixPath(current) {
		items = append(items, Item{
			Path: current,
			Desc: "your login shell is Helix — it will be set back to " + fallbackShell(home),
			Kind: KindShell,
		})
	}
	for _, line := range helixLinesIn(etcShells) {
		items = append(items, Item{
			Path: etcShells,
			// The line itself is carried in EditLine; Desc is for the human.
			// They were the same field once and the manifest rendered
			// "/etc/shells" with "/usr/local/bin/helix" beneath it, which reads
			// as a second path being deleted rather than as a line being cut
			// out of the first.
			Desc:      "removes the line " + line + ", leaving every other shell registered",
			EditLine:  line,
			Kind:      KindShell,
			Edit:      true,
			NeedsRoot: needsRoot(etcShells),
		})
	}
	return items
}

// planServices returns the OS service units, at the SAME paths daemon_cmd.go
// writes them to. Kept literal rather than imported because internal/uninstall
// must not depend on cmd/helix; uninstall_test.go pins them against the source
// of truth so the two cannot drift apart silently.
func planServices(home string) []Item {
	switch runtime.GOOS {
	case "darwin":
		return []Item{{
			Path: filepath.Join(home, "Library", "LaunchAgents", "com.helix.daemon.plist"),
			Desc: "the launchd background service",
		}}
	case "linux":
		return []Item{{
			Path: filepath.Join(home, ".config", "systemd", "user", "helix-daemon.service"),
			Desc: "the systemd user service",
		}}
	}
	return nil
}

// removeShellItem either edits /etc/shells or puts the login shell back.
func removeShellItem(it Item) error {
	if it.Edit {
		return dropLine(it.Path, it.EditLine)
	}
	return restoreLoginShell()
}

// restoreLoginShell points the account back at a real shell.
//
// The target comes from ~/.helix/shell_pref, which the INSTALLER wrote for
// exactly this purpose — it recorded $SHELL before chsh replaced it, so it is
// the shell the user actually had rather than a guess. If that file is gone,
// fall back to the first shell on the machine that exists.
//
// chsh prompts for a password. That is not a defect to work around: changing a
// login shell without authenticating would be the defect.
func restoreLoginShell() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target := fallbackShell(home)
	if target == "" {
		return errors.New("no replacement shell found on this machine")
	}
	cmd := exec.Command("chsh", "-s", target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("chsh -s %s: %w", target, err)
	}
	return nil
}

// fallbackShell is the shell to restore: the installer's record first, then the
// usual suspects, and only ones that exist.
func fallbackShell(home string) string {
	if raw, err := os.ReadFile(filepath.Join(home, ".helix", "shell_pref")); err == nil {
		if s := strings.TrimSpace(string(raw)); s != "" && !isHelixPath(s) && exists(s) {
			return s
		}
	}
	for _, s := range []string{"/bin/zsh", "/bin/bash", "/bin/sh"} {
		if exists(s) {
			return s
		}
	}
	return ""
}

// loginShell reads the account's shell without shelling out to dscl or getent:
// $SHELL is what every other part of Helix already trusts for this.
func loginShell() string { return strings.TrimSpace(os.Getenv("SHELL")) }

// isHelixPath reports whether a path names the Helix binary.
func isHelixPath(p string) bool {
	base := filepath.Base(strings.TrimSpace(p))
	return base == "helix" || base == "helix.exe"
}

// helixLinesIn returns every line of a shells file that names Helix.
func helixLinesIn(path string) []string {
	raw, err := os.ReadFile(path) // #nosec G304 -- a fixed system path
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") && isHelixPath(l) {
			out = append(out, l)
		}
	}
	return out
}

// needsRoot reports whether removing path requires elevation.
//
// Probes the PARENT DIRECTORY, because deleting a file is a write to the
// directory that contains it, not to the file. /usr/local/bin/helix is
// root-owned and so is /usr/local/bin, but the check that matters is the
// second one — and on a Homebrew machine where /usr/local/bin is user-writable,
// a root-owned binary inside it is removable without sudo. Testing the file
// would ask for a password nobody needed to type.
func needsRoot(path string) bool {
	if os.Geteuid() == 0 {
		return false
	}
	target := filepath.Dir(path)
	if _, err := os.Lstat(path); err != nil {
		return false
	}
	return syscall.Access(target, 2 /* W_OK */) != nil
}

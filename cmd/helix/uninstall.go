// cmd/helix/uninstall.go
// Purpose: the one uninstall flow, with three doors into it —
// `helix uninstall`, `make uninstall` (via scripts/uninstall.sh), and /purge's
// third confirmation.
//
// ONE FLOW, because the alternative was three. `make install` is a shell
// script, so the obvious shape was a matching `scripts/uninstall.sh` that knew
// the paths, plus Go that knew them again for /purge. That is the drift this
// repo keeps paying for, and here a drifted copy does not misreport a port: it
// leaves a launchd job pointing at a binary that no longer exists, retried and
// logged forever. So the script is a wrapper and this is the implementation.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"helix/internal/shell"
	"helix/internal/uninstall"
)

// uninstallOptions tune one run.
type uninstallOptions struct {
	// assumeYes skips the confirmation. For scripts/uninstall.sh, which has
	// already asked — never wired to anything a voice transcript can reach.
	assumeYes bool

	// keepData leaves ~/.helix alone. /purge uses this: it has just deleted
	// that content itself, item by item, with its own manifest.
	keepData bool
}

// handleUninstall runs the full removal. Returns true when anything was
// removed, so /purge can tailor its closing line.
func handleUninstall(opts uninstallOptions) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		uiFail("uninstall", "cannot resolve the home directory: "+err.Error())
		return false
	}

	items := uninstall.Plan(home, installedBinaryPath())
	if opts.keepData {
		items = withoutKind(items, uninstall.KindData)
	}
	if len(items) == 0 {
		fmt.Println(shell.Step(shell.StateGood, "nothing to uninstall",
			"no Helix binary, service or registration was found on this machine"))
		return false
	}

	printUninstallManifest(home, items)

	if !opts.assumeYes {
		if !wizConfirmDanger(fmt.Sprintf("permanently remove these %d item(s)", len(items))) {
			fmt.Println(shell.Step(shell.StateIdle, "cancelled", "nothing was removed"))
			return false
		}
	}

	// Elevation is requested ONCE, before anything is touched, rather than
	// discovered halfway through. A password prompt appearing after four
	// deletions is how a user ends up cancelling in the middle.
	if uninstall.NeedsRootAny(items) && os.Geteuid() != 0 {
		return reexecWithSudo(opts)
	}

	removed, failures := uninstall.Apply(items)
	printUninstallResult(removed, failures)
	return removed > 0
}

// withoutKind filters a plan.
func withoutKind(items []uninstall.Item, k uninstall.Kind) []uninstall.Item {
	out := items[:0:0]
	for _, it := range items {
		if it.Kind != k {
			out = append(out, it)
		}
	}
	return out
}

// installedBinaryPath is the Helix on PATH, falling back to this process.
//
// PATH FIRST, and this ordering is the whole point. `make uninstall` runs
// ./dist/helix — a build artifact nobody installed — and if that removed
// itself it would report success while /usr/local/bin/helix, the one the user
// actually types, survived untouched. What is being uninstalled is the
// INSTALLED copy, whichever binary is asking.
func installedBinaryPath() string {
	if p, err := exec.LookPath("helix"); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return p
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return ""
}

// printUninstallManifest shows what will go, grouped, before the yes.
//
// Grouped for the reason purge.go's manifest is: an undifferentiated list makes
// the one irreversible line exactly as prominent as the cache next to it. Here
// the login shell leads, because it is the only row that can leave someone
// unable to open a terminal.
func printUninstallManifest(home string, items []uninstall.Item) {
	fmt.Println(shell.PanelTitle("uninstall"))
	for _, k := range []uninstall.Kind{
		uninstall.KindShell, uninstall.KindService,
		uninstall.KindData, uninstall.KindBinary,
	} {
		group := ofKind(items, k)
		if len(group) == 0 {
			continue
		}
		fmt.Println(shell.PanelLine(shell.Fg(shell.HexText, k.String())))
		for _, it := range group {
			line := "  " + shortHomePath(home, it.Path)
			if it.Edit {
				line += shell.Muted("  ·  one line removed, the file is kept")
			}
			if it.NeedsRoot {
				line += shell.Muted("  ·  needs sudo")
			}
			fmt.Println(shell.PanelLine(line))
			fmt.Println(shell.PanelLine(shell.Muted("    " + it.Desc)))
		}
	}
	fmt.Println(shell.PanelLine(""))
	fmt.Println(shell.PanelLine(shell.Muted(
		"Not touched: Ollama, sox, ffmpeg and any other tool Helix asked you to install.")))
	fmt.Println(shell.PanelLine(shell.Muted(
		"Those are other people's programs and you may be using them for something else.")))
	fmt.Println(shell.PanelEnd())
}

func ofKind(items []uninstall.Item, k uninstall.Kind) []uninstall.Item {
	var out []uninstall.Item
	for _, it := range items {
		if it.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

// shortHomePath abbreviates $HOME to ~, the same way /purge's manifest does.
func shortHomePath(home, path string) string {
	if home != "" && strings.HasPrefix(path, home+string(os.PathSeparator)) {
		return "~" + path[len(home):]
	}
	return path
}

// reexecWithSudo re-runs `helix uninstall --yes` as root.
//
// Re-executing rather than shelling out to `sudo rm` per path: one password
// prompt, one implementation, and the elevated process runs the SAME plan
// rather than a shell transcription of it. --yes is safe here only because the
// confirmation has already been given in this process, immediately above.
func reexecWithSudo(opts uninstallOptions) bool {
	exe, err := os.Executable()
	if err != nil {
		uiFail("uninstall", "cannot locate this binary to elevate: "+err.Error())
		return false
	}
	args := []string{exe, "uninstall", "--yes"}
	if opts.keepData {
		args = append(args, "--keep-data")
	}
	fmt.Println(shell.Hint("some items are root-owned — re-running with sudo"))
	cmd := exec.Command("sudo", args...) // #nosec G204 -- args are fixed literals plus our own path
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		uiFail("uninstall", "elevated run failed: "+err.Error())
		return false
	}
	return true
}

// printUninstallResult reports what happened.
func printUninstallResult(removed int, failures []string) {
	if len(failures) == 0 {
		uiOK("uninstalled", fmt.Sprintf("%d item(s) removed", removed))
		fmt.Println(shell.Hint("open a new terminal for the PATH change to take effect"))
		return
	}
	uiWarn("uninstall", fmt.Sprintf("%d removed, %d could not be", removed, len(failures)))
	for _, f := range failures {
		fmt.Println(shell.PanelLine(shell.Muted("  " + f)))
	}
}

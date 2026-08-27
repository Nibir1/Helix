// cmd/helix/reboot_update.go
//
// Purpose: the update half of /reboot — check, show, confirm, install.
//
// Two rules shape everything here, and both are refusals:
//
//   - **A spoken reboot never installs.** `/reboot` is voice-reachable because
//     restarting destroys nothing; downloading and executing a new binary is a
//     different act entirely, and a television saying "reboot" must not be able
//     to cause it. The spoken path checks and REPORTS, and the install waits
//     for a typed confirmation.
//   - **An update never blocks the restart.** GitHub being down, a rate limit,
//     a checksum that does not match — none of them may stop `/reboot` from
//     doing the thing it was asked to do. Every failure here degrades to "no
//     update" and the shell restarts anyway.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"helix/internal/config"
	"helix/internal/shell"
	"helix/internal/update"
	"helix/internal/utils"
)

// updateCheckTimeout bounds the whole check.
//
// Short on purpose: this runs on the path of a restart the user asked for, and
// a slow network must cost seconds, not a hung shell.
const updateCheckTimeout = 12 * time.Second

// rebootUpdatedEnv tells the child that it is the FIRST run of a freshly
// installed binary, so the supervisor can roll back if it dies immediately.
const rebootUpdatedEnv = "HELIX_REBOOT_UPDATED"

// checkForUpdate and confirmInstall are seams, so the two rules that matter —
// a spoken reboot never installs, and a declined confirmation never installs —
// are testable without a network or a terminal. Nothing but tests reassigns
// them; they are variables rather than parameters because every caller of
// maybeInstallUpdate wants the real ones.
var (
	checkForUpdate = update.Check
	confirmInstall = wizConfirmDanger
	performInstall = installCandidate
)

// maybeInstallUpdate looks for a newer Helix and, with permission, installs it.
//
// Returns whether an install happened, which the caller uses only to decide
// what to say — the restart proceeds either way.
//
// Args:
//   - spoken: whether the reboot was asked for out loud. Gates the install.
//   - explicit: whether the user asked to check ("/reboot check"), which makes
//     "you are up to date" worth printing rather than noise.
func maybeInstallUpdate(spoken, explicit bool) bool {
	if !cfg.Update.Check && !explicit {
		return false
	}

	current, err := update.ParseVersion(config.HelixVersion)
	if err != nil {
		// An unreadable own version means every comparison is meaningless, so
		// nothing is offered. Silent unless asked: a user who typed /reboot
		// wants a restart, not a lecture about a constant.
		if explicit {
			fmt.Println(shell.Step(shell.StateWarn, "update",
				"cannot read this build's own version: "+err.Error()))
		}
		return false
	}

	self, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	defer cancel()

	candidate, err := checkForUpdate(ctx, update.Options{
		Current:    current,
		Channel:    cfg.Update.Channel,
		Repo:       cfg.Update.Repo,
		LocalPaths: cfg.Update.LocalPaths,
		SelfPath:   self,
	})
	if candidate == nil {
		if err != nil && explicit {
			fmt.Println(shell.Step(shell.StateWarn, "update check failed", err.Error()))
			fmt.Println(shell.Hint("/reboot now restarts without checking"))
		} else if explicit {
			fmt.Println(shell.Step(shell.StateGood, "up to date",
				"running "+current.String()))
		}
		return false
	}

	printUpdatePanel(candidate, current)

	if spoken {
		// The carve-out. Reported, never acted on.
		fmt.Println(shell.Step(shell.StateIdle, "not installing",
			"a spoken reboot restarts only — type /reboot to install this"))
		return false
	}
	if !confirmInstall(fmt.Sprintf("install %s and restart into it", candidate.Describe())) {
		fmt.Println(shell.Step(shell.StateIdle, "kept", "the update was not installed"))
		return false
	}
	return performInstall(candidate)
}

// installCandidate fetches, verifies and installs, reporting each refusal.
func installCandidate(c *update.Candidate) bool {
	self, err := os.Executable()
	if err != nil {
		fmt.Println(shell.Step(shell.StateBad, "install", "cannot locate this binary: "+err.Error()))
		return false
	}
	// Checked before anything is downloaded: a Helix installed under
	// /usr/local/bin is not writable by the user running it, and finding that
	// out after a 40 MB download is a worse experience than a clear refusal.
	if err := update.WritableTarget(self); err != nil {
		fmt.Println(shell.Step(shell.StateBad, "install", err.Error()))
		fmt.Println(shell.StepCommand("sudo helix ... is NOT the answer — reinstall with your package manager"))
		return false
	}

	source := c.Path
	if c.Source == update.SourceGitHub {
		staging, err := os.MkdirTemp(filepath.Dir(self), ".helix-update-*")
		if err != nil {
			fmt.Println(shell.Step(shell.StateBad, "download", err.Error()))
			return false
		}
		defer func() { _ = os.RemoveAll(staging) }()

		fmt.Println(shell.Step(shell.StateIdle, "downloading", c.Tag))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		unreg := registerCancel(cancel)
		source, err = update.Fetch(ctx, c, staging)
		unreg()
		if err != nil {
			fmt.Println(shell.Step(shell.StateBad, "download", err.Error()))
			return false
		}
		fmt.Println(shell.Step(shell.StateGood, "verified", "checksum matches the release manifest"))
	}

	backup, err := update.Install(source, self)
	if err != nil {
		fmt.Println(shell.Step(shell.StateBad, "install", err.Error()))
		return false
	}
	fmt.Println(shell.Step(shell.StateGood, "installed", c.Describe()))
	if backup != "" {
		fmt.Println(shell.KV("ROLLBACK", shell.Muted("the previous binary is kept at ")+
			shell.Value(backup), shell.KVWidth("ROLLBACK")))
	}
	return true
}

// printUpdatePanel shows what is on offer before asking.
func printUpdatePanel(c *update.Candidate, current update.Version) {
	fmt.Println(shell.PanelTitle("update available"))

	w := shell.KVWidth("RUNNING", "AVAILABLE", "SOURCE", "SIZE", "PUBLISHED")
	fmt.Println(shell.KV("RUNNING", shell.Muted(current.String()), w))
	if c.VersionKnown {
		fmt.Println(shell.KV("AVAILABLE", shell.Value(c.Version.String())+
			shell.Muted("  "+c.Notes), w))
	} else {
		// Said plainly rather than dressed up as a version. A local build with
		// no stamped version is a NEWER FILE, and claiming a version number for
		// it would be inventing one.
		fmt.Println(shell.KV("AVAILABLE", shell.Value("a newer local build")+
			shell.Muted("  no stamped version — compared by file time"), w))
	}
	fmt.Println(shell.KV("SOURCE", shell.Muted(string(c.Source)+"  ·  "+c.Notes), w))
	if c.Size > 0 {
		fmt.Println(shell.KV("SIZE", shell.Muted(compactBytes(c.Size)), w))
	}
	if !c.Published.IsZero() {
		fmt.Println(shell.KV("PUBLISHED", shell.Muted(c.Published.Format("2006-01-02 15:04")), w))
	}

	if advisory := c.Advisory(cfg.Update.Repo); advisory != "" {
		fmt.Println(shell.PanelGap())
		for _, l := range shell.PanelWrap(advisory, shell.Muted) {
			fmt.Println(l)
		}
	}
	fmt.Println(shell.PanelEnd())
}

// registerCancel wires a cancel func to Ctrl+C, so a long download is
// interruptible without killing the shell rather than being unstoppable.
func registerCancel(cancel context.CancelFunc) func() {
	return utils.RegisterOperation(cancel)
}

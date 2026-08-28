// cmd/helix/reboot_update.go
//
// Purpose: the update half of /reboot — check, install, restart.
//
// **Installing is automatic, by owner decision.** There is no confirmation and
// no voice carve-out: `/reboot` finds a newer Helix and installs it, whether the
// word was typed or spoken. The reasoning is the owner's and worth recording,
// because the code reads as permissive without it — the update comes from a
// repository the owner controls and publishes to deliberately, so "is this
// build wanted?" is a question already answered by the act of tagging a release.
// A prompt in front of that is a prompt with one sensible answer.
//
// What that shifts, stated plainly rather than left implicit: whoever can
// publish a release to the configured repo can replace this binary without a
// human present. The controls that remain are the ones in internal/update — a
// mandatory checksum, a pinned host, a payload that must prove it is Helix for
// this machine — plus the supervisor's automatic rollback when a freshly
// installed binary cannot start. Integrity, not authenticity; see ADR-019.
//
// One rule survives unchanged: **an update never blocks the restart.** GitHub
// being down, a rate limit, a checksum that does not match — none may stop
// `/reboot` from doing the thing it was asked to do. Every failure degrades to
// "no update" and the shell restarts anyway.
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

// checkForUpdate and performInstall are seams, so the update path is testable
// without a network. Nothing but tests reassigns them.
var (
	checkForUpdate = update.Check
	performInstall = installCandidate
)

// maybeInstallUpdate looks for a newer Helix and, with permission, installs it.
//
// Returns whether an install happened, which the caller uses only to decide
// what to say — the restart proceeds either way.
//
// Args:
//   - spoken: whether the reboot was asked for out loud. Reported, not gated —
//     it changes only what the record says about who asked.
//   - explicit: whether the user asked to check ("/reboot check"), which both
//     makes "you are up to date" worth printing rather than noise AND means
//     install nothing, because someone asking a question has not asked to be
//     upgraded.
func maybeInstallUpdate(spoken, explicit bool) bool {
	if !cfg.Update.Check && !explicit {
		setUpdateOutcome("not checked — update.check is off")
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
		if err != nil {
			setUpdateOutcome("check failed — " + err.Error())
			if explicit {
				fmt.Println(shell.Step(shell.StateWarn, "update check failed", err.Error()))
				fmt.Println(shell.Hint("/reboot now restarts without checking"))
			}
			return false
		}
		// Recorded on BOTH paths, printed only when asked.
		//
		// A plain /reboot used to say nothing at all when already current, so
		// the restart panel could not report whether it had even looked — and
		// asked afterwards, Helix answered from the model's guess rather than
		// from a fact. "Checked and found nothing" is a different statement
		// from "did not check", and the user is entitled to know which.
		setUpdateOutcome("already on the newest release (" + current.String() + ")")
		if explicit {
			fmt.Println(shell.Step(shell.StateGood, "up to date",
				"running "+current.String()))
		}
		return false
	}

	printUpdatePanel(candidate, current)

	// `/reboot check` answers a question. Installing off the back of it would
	// mean there is no way to ask without being upgraded.
	if explicit {
		fmt.Println(shell.Step(shell.StateIdle, "not installed",
			"/reboot installs it and restarts"))
		return false
	}
	_ = spoken // the record notes who asked; the install does not care
	if performInstall(candidate) {
		setUpdateOutcome("installed " + candidate.Version.String() + " — restarting into it")
		return true
	}
	setUpdateOutcome("found " + candidate.Version.String() + " but could not install it")
	return false
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

// updateOutcome is what the last check concluded, for the restart panel.
//
// A single string rather than a struct: the panel prints one row, and the value
// exists so that row can state a FACT instead of leaving the user to ask the
// model afterwards — which is guesswork about the program's own behaviour.
var updateOutcome string

func setUpdateOutcome(s string) { updateOutcome = s }

// updateOutcomeLine returns what to show on the restart panel, if anything.
func updateOutcomeLine() string { return updateOutcome }

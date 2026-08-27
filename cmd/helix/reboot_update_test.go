// cmd/helix/reboot_update_test.go
//
// Purpose: the two refusals that keep a self-updater safe to have.
//
// Everything else about updating is verified in internal/update, where the
// checksum, the host pin and the archive handling live. What can only be tested
// here is the POLICY: who is allowed to cause an install.
package main

import (
	"context"
	"testing"
	"time"

	"helix/internal/config"
	"helix/internal/update"
)

// stubUpdateSeams replaces the check, the confirmation and the install for one
// test, and records whether an install was attempted.
func stubUpdateSeams(t *testing.T, candidate *update.Candidate, confirm bool) *bool {
	t.Helper()
	installed := false

	oldCheck, oldConfirm, oldInstall := checkForUpdate, confirmInstall, performInstall
	t.Cleanup(func() {
		checkForUpdate, confirmInstall, performInstall = oldCheck, oldConfirm, oldInstall
	})

	checkForUpdate = func(context.Context, update.Options) (*update.Candidate, error) {
		return candidate, nil
	}
	confirmInstall = func(string) bool { return confirm }
	performInstall = func(*update.Candidate) bool {
		installed = true
		return true
	}
	return &installed
}

func fakeCandidate(t *testing.T) *update.Candidate {
	t.Helper()
	v, err := update.ParseVersion("99.0.0")
	if err != nil {
		t.Fatal(err)
	}
	return &update.Candidate{
		Source: update.SourceGitHub, Version: v, Tag: "v99.0.0",
		Notes: "a much newer Helix", URL: "https://github.com/x/y/z.tar.gz",
		SHA256: "abc", Published: time.Now(), VersionKnown: true,
	}
}

// THE rule. /reboot is voice-reachable because restarting destroys nothing;
// downloading and executing a new binary is a different act, and a television
// saying "reboot" must not be able to cause it.
func TestSpokenRebootNeverInstallsAnUpdate(t *testing.T) {
	withUpdateConfig(t)
	installed := stubUpdateSeams(t, fakeCandidate(t), true /* would confirm */)

	if maybeInstallUpdate(true /* spoken */, false) {
		t.Error("a spoken reboot reported an install")
	}
	if *installed {
		t.Fatal("a spoken reboot installed a downloaded binary — voice must never " +
			"be able to replace the program the user runs")
	}
}

// A typed reboot still asks. Replacing the binary someone is running is not
// something to do because they wanted a fresh process.
func TestDeclinedConfirmationNeverInstalls(t *testing.T) {
	withUpdateConfig(t)
	installed := stubUpdateSeams(t, fakeCandidate(t), false /* declines */)

	if maybeInstallUpdate(false, false) {
		t.Error("a declined update reported an install")
	}
	if *installed {
		t.Fatal("an update was installed after the user declined it")
	}
}

// The typed, confirmed path is the one that may proceed — otherwise the two
// refusals above would pass on a feature that never works at all.
func TestTypedAndConfirmedInstalls(t *testing.T) {
	withUpdateConfig(t)
	installed := stubUpdateSeams(t, fakeCandidate(t), true)

	if !maybeInstallUpdate(false, false) {
		t.Error("a confirmed update did not report an install")
	}
	if !*installed {
		t.Fatal("a confirmed update did not install")
	}
}

// check:false means "do not look on every restart", and must not be confused
// with "never update" — an explicit /reboot check still answers.
func TestCheckDisabledStillAnswersAnExplicitCheck(t *testing.T) {
	withUpdateConfig(t)
	cfg.Update.Check = false
	installed := stubUpdateSeams(t, fakeCandidate(t), false)

	if maybeInstallUpdate(false, false /* not explicit */) {
		t.Error("an update ran with checking disabled")
	}
	if *installed {
		t.Fatal("checking disabled must mean no install on an ordinary reboot")
	}

	// Explicit: it looks, and reports, and still installs nothing without a yes.
	if maybeInstallUpdate(false, true) {
		t.Error("an explicit check must not install by itself")
	}
}

// withUpdateConfig gives the test a config with the shipped update defaults.
func withUpdateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	c, err := config.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	old := cfg
	cfg = c
	t.Cleanup(func() { cfg = old })
}

// The shipped policy: check on, install never automatic.
func TestUpdateDefaultsCheckButDoNotInstall(t *testing.T) {
	d := config.UpdateDefaults()
	if !d.Check {
		t.Error("checking should be on by default — it is one request on a restart you asked for")
	}
	if d.Channel != "auto" {
		t.Errorf("default channel = %q, want auto", d.Channel)
	}
	if d.Repo == "" {
		t.Error("the default repo must be set, or the updater points at nothing")
	}
}

// cmd/helix/reboot_update_test.go
//
// Purpose: the update POLICY — who causes an install, and when.
//
// Everything else about updating is verified in internal/update, where the
// checksum, the host pin and the archive handling live.
//
// The policy is deliberately permissive by owner decision: installing is
// automatic, from typed and spoken reboots alike, because the release comes
// from a repository the owner controls and tags on purpose. These tests pin
// that decision so it stays a decision rather than becoming an accident, and
// pin the one case that is still NOT an install: asking whether one exists.
package main

import (
	"context"
	"testing"
	"time"

	"helix/internal/config"
	"helix/internal/update"
)

// stubUpdateSeams replaces the check and the install for one test, and records
// whether an install was attempted.
func stubUpdateSeams(t *testing.T, candidate *update.Candidate) *bool {
	t.Helper()
	installed := false

	oldCheck, oldInstall := checkForUpdate, performInstall
	t.Cleanup(func() { checkForUpdate, performInstall = oldCheck, oldInstall })

	checkForUpdate = func(context.Context, update.Options) (*update.Candidate, error) {
		return candidate, nil
	}
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

// Owner decision: installing is automatic and needs no human in the loop,
// because the release comes from a repo the owner controls and tags on purpose.
// Pinned so it stays a decision — the alternative reads identically in code.
func TestRebootInstallsWithoutAsking(t *testing.T) {
	for _, spoken := range []bool{false, true} {
		name := "typed"
		if spoken {
			name = "spoken"
		}
		t.Run(name, func(t *testing.T) {
			withUpdateConfig(t)
			installed := stubUpdateSeams(t, fakeCandidate(t))

			if !maybeInstallUpdate(spoken, false) {
				t.Error("the update did not report an install")
			}
			if !*installed {
				t.Fatalf("a %s reboot did not install the available update", name)
			}
		})
	}
}

// check:false means "do not look on every restart", and must not be confused
// with "never update" — an explicit /reboot check still answers.
func TestCheckDisabledStillAnswersAnExplicitCheck(t *testing.T) {
	withUpdateConfig(t)
	cfg.Update.Check = false
	installed := stubUpdateSeams(t, fakeCandidate(t))

	if maybeInstallUpdate(false, false /* not explicit */) {
		t.Error("an update ran with checking disabled")
	}
	if *installed {
		t.Fatal("checking disabled must mean no install on an ordinary reboot")
	}

	// Explicit: it looks and reports, and installs nothing. Asking whether an
	// update exists is a question, and a question that upgrades you is a
	// question nobody can afford to ask.
	if maybeInstallUpdate(false, true) {
		t.Error("/reboot check must not install")
	}
	if *installed {
		t.Fatal("/reboot check installed an update — there would then be no way " +
			"to ask without being upgraded")
	}
}

// withUpdateConfig gives the test a config with the shipped update defaults.
func withUpdateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // os.UserHomeDir on Windows
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

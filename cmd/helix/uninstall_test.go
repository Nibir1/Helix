// cmd/helix/uninstall_test.go
// Purpose: the wiring an uninstall depends on, asserted without running one.
//
// The removal itself is covered in internal/uninstall against temp directories.
// What is left here is routing and ordering — which binary gets removed, that
// /purge asks separately, and that the sudo re-exec cannot be turned into an
// unattended one.
package main

import (
	"os"
	"strings"
	"testing"
)

// THE ORDERING BUG THIS PREVENTS. `make uninstall` runs ./dist/helix — a build
// artifact nobody installed. If the plan targeted os.Executable() first, that
// run would delete the build output, report success, and leave
// /usr/local/bin/helix — the one the user actually types — untouched.
func TestTheINSTALLEDBinaryIsTargetedNotTheRunningOne(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "uninstall.go"), "func installedBinaryPath("))
	lookPath := strings.Index(body, "exec.LookPath")
	executable := strings.Index(body, "os.Executable")
	if lookPath < 0 {
		t.Fatal("installedBinaryPath does not consult PATH at all")
	}
	if executable >= 0 && executable < lookPath {
		t.Error("os.Executable is consulted BEFORE PATH; `make uninstall` would delete " +
			"./dist/helix and leave the installed copy in place, reporting success")
	}
}

// /purge must ask SEPARATELY. Folding uninstall into the first confirmation
// takes the shell away from someone who ran /purge in order to keep using it.
func TestPurgeAsksForUninstallSeparately(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "purge.go"), "func handlePurgeCommand("))
	if !strings.Contains(body, "handleUninstall(") {
		t.Fatal("/purge never offers the uninstall")
	}
	if strings.Count(body, "wizConfirmDanger") < 3 {
		t.Errorf("/purge has %d danger confirmations, want 3 (data, weights, uninstall) — "+
			"the uninstall is not being asked for on its own",
			strings.Count(body, "wizConfirmDanger"))
	}
	// The data is already gone by then, item by item, with its own manifest.
	if !strings.Contains(body, "keepData: true") {
		t.Error("/purge's uninstall would re-plan the Helix home it has just emptied")
	}
	// And the offer must come after the deletion report, not before it.
	if strings.Index(body, "printPurgeResult") > strings.Index(body, "handleUninstall(") {
		t.Error("the uninstall is offered before the purge has reported what it did")
	}
}

// A prompt whose yes does nothing teaches people that yes does not matter.
// purge.go documents paying for exactly that with its weights prompt.
func TestTheUninstallOfferIsSkippedWhenThereIsNothingToRemove(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "purge.go"), "func handlePurgeCommand("))
	if !strings.Contains(body, "uninstallOffered()") {
		t.Error("/purge offers an uninstall without checking there is anything left to uninstall")
	}
}

// --yes exists for the elevated re-exec, where consent was already given in the
// parent. It must never be reachable from a transcript: ADR-005 rule 2 makes a
// typed confirmation mandatory for destructive actions, and this is the most
// destructive one there is.
func TestUninstallIsNotReachableByVoice(t *testing.T) {
	reg := readSourceFile(t, "registry_tables.go")
	if strings.Contains(reg, `Name: "/uninstall"`) {
		t.Fatal("an /uninstall slash command exists; if it is ever added it must be " +
			"VoiceOK:false and on the denied list, and this test updated deliberately")
	}
	// The subcommand is the only door, and subcommands are not transcripts.
	body := readSourceFile(t, "daemon_cmd.go")
	if !strings.Contains(body, `case "uninstall":`) {
		t.Error("the uninstall subcommand is gone")
	}
	// /purge itself is already denied by voice; assert that has not slipped.
	if !strings.Contains(reg, `Name: "/purge"`) {
		t.Fatal("/purge is missing from the registry")
	}
}

// The manifest is the only thing that makes the answer informed, so the
// make-target path must not skip it.
func TestTheMakeTargetDoesNotAssumeYes(t *testing.T) {
	src, err := os.ReadFile("../../scripts/uninstall.sh")
	if err != nil {
		t.Fatal(err)
	}
	// Comments stripped first. The script explains at length WHY it does not
	// pass --yes, and the first version of this test matched that explanation
	// and failed on it — a guard that reads prose as code.
	var code []string
	for _, line := range strings.Split(string(src), "\n") {
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		code = append(code, line)
	}
	body := strings.Join(code, "\n")
	if strings.Contains(body, "--yes") || strings.Contains(body, " -y ") {
		t.Error("scripts/uninstall.sh skips the confirmation; `make uninstall` would remove " +
			"a login shell without showing what it was about to do")
	}
	if !strings.Contains(body, "uninstall") {
		t.Error("scripts/uninstall.sh does not invoke the binary's uninstall")
	}
	// It must prefer the INSTALLED binary, for the same reason as above.
	if !strings.Contains(body, "command -v") {
		t.Error("scripts/uninstall.sh does not look for the installed binary first")
	}
}

// Ollama, sox and ffmpeg are other people's programs. Removing them because
// Helix once suggested installing them would be overreach.
func TestThirdPartyToolsAreLeftAlone(t *testing.T) {
	body := readSourceFile(t, "uninstall.go")
	if !strings.Contains(body, "Not touched: Ollama") {
		t.Error("the manifest does not say which third-party tools survive; a user cannot " +
			"tell whether their Ollama models are about to go")
	}
	for _, tool := range []string{"ollama", "sox", "ffmpeg"} {
		plan := readSourceFile(t, "../../internal/uninstall/uninstall.go")
		if strings.Contains(strings.ToLower(plan), `"`+tool+`"`) {
			t.Errorf("the uninstall plan names %q; Helix does not own it", tool)
		}
	}
}

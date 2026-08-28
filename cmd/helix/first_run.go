// cmd/helix/first_run.go
//
// Purpose: make the first boot of a precompiled Helix binary produce a WORKING
// Helix, not a configured one.
//
// The old first run asked exactly one question — which AI provider — and
// stopped. Everything else was discovered later, by failing: `/blackbox on`
// refused because sox was missing, the camera stayed dark because ffmpeg was
// missing, and the STT/TTS chain was unset until someone found the setup
// wizard. Each of those printed a correct `brew install …` hint at the moment
// of failure, which is the worst possible moment: the user came to try a
// feature, not to shop for packages.
//
// So the stages run in dependency order — brain, then body, then senses:
//
//  1. AI provider   (already existed; without it nothing works)
//  2. System packages (new; without them voice and vision cannot work)
//  3. Speech chain  (already existed; needs 2 to be worth configuring)
//
// Consent rules match the sidecar installers exactly, because they are the
// same promise: nothing installs implicitly, every install is one separate
// yes, the exact command is shown before it runs, and any stage can be skipped
// without derailing the rest. A setup flow that cannot be escaped is a setup
// flow people kill the terminal to escape.
package main

import (
	"fmt"
	"time"

	"helix/internal/commands"
	"helix/internal/deps"
	"helix/internal/shell"
)

// runFirstRunStages runs the setup that follows the AI provider.
//
// It is separate from the provider stage for a boring but load-bearing reason:
// the provider must be chosen before the model loads, while the speech wizard
// cannot run until speech.Init has built the registry. They sit on opposite
// sides of startup, so first run is two calls, not one — and the banner lives
// here, with the stages the user is about to see.
//
// Args: none. Returns: nothing; every stage is skippable and reports itself.
// Complexity: interactive.
func runFirstRunStages() {
	fmt.Println()
	// Announce only what will actually happen. "Two more stages" followed by
	// one stage — because the packages were already installed — reads as a
	// stage that silently failed.
	if len(deps.Missing()) > 0 {
		fmt.Println(shell.PanelTitle("first run"))
		fmt.Println(shell.PanelLine(shell.Muted("two more stages: the system packages Helix needs, then the speech chain")))
		fmt.Println(shell.PanelLine(shell.Muted("either can be skipped and redone later with /setup")))
		fmt.Println(shell.PanelEnd())
	} else {
		fmt.Println(shell.PanelTitle("first run"))
		fmt.Println(shell.PanelLine(shell.Muted("one more stage: system packages are already installed, only the speech chain is left")))
		fmt.Println(shell.PanelLine(shell.Muted("it can be skipped and redone later with /setup")))
		fmt.Println(shell.PanelEnd())
	}

	runDependencySetup(true)
	offerSpeechSetup()
}

// runDependencySetup reports what the host is missing and offers to fix it.
//
// Args: firstRun: softens the wording and skips the "nothing to do" line, which
// is noise during setup but is the whole answer when invoked deliberately.
// Returns: whether every catalog dependency is present when it finishes.
func runDependencySetup(firstRun bool) bool {
	missing := deps.Missing()
	if len(missing) == 0 {
		if !firstRun {
			uiOK("system packages", "all of them are already installed")
		}
		return true
	}

	fmt.Println()
	fmt.Println(shell.PanelTitle("system packages"))
	for _, d := range missing {
		fmt.Println(shell.KV(d.Name, shell.Badge(shell.StateWarn, "missing")+
			shell.Muted("  "+d.Purpose), shell.KVWidth(d.Name)))
	}

	manager := deps.DetectManager()
	if manager == deps.ManagerUnknown {
		fmt.Println(shell.PanelEnd())
		uiDetail(deps.ManagerHint())
		return false
	}
	fmt.Println(shell.KV("MANAGER", shell.Value(string(manager)), shell.KVWidth("MANAGER")))
	fmt.Println(shell.PanelEnd())

	allPresent := true
	for _, d := range missing {
		cmd, ok := d.InstallCommand(manager)
		if !ok {
			// Honest dead end, same as the sidecar installers: name the tool
			// and stop, rather than run a guessed package name under Helix's
			// name. See the Windows/sox note in the deps catalog.
			uiWarn(d.Name, fmt.Sprintf("no verified install command for %s on %s",
				d.Name, manager))
			uiDetail("Install it yourself, then re-run /setup.")
			allPresent = false
			continue
		}

		fmt.Println()
		uiIdle(d.Name, d.Purpose)
		fmt.Println(shell.StepCommand(cmd))
		if !commands.AskForConfirmation(fmt.Sprintf("Install %s now?", d.Name)) {
			uiIdle("skipped", fmt.Sprintf("%s stays unavailable until %s is installed",
				capabilityOf(d), d.Name))
			allPresent = false
			continue
		}
		// Same 30-minute budget the sidecar installs use: a package manager
		// compiling from source is slow, not stuck.
		if !runVisibleCommand(cmd, 30*time.Minute) {
			allPresent = false
			continue
		}
		// Trust the lookup, not the exit code — a manager can succeed and still
		// leave the binary somewhere this process cannot see yet.
		if !d.Present() {
			uiWarn(d.Name, "installed, but still not on PATH")
			uiDetail("Open a new shell and run /setup to continue.")
			allPresent = false
			continue
		}
		uiOK(d.Name, "installed")
	}
	return allPresent
}

// capabilityOf names what a skipped dependency costs, in the user's terms.
func capabilityOf(d deps.Dependency) string {
	switch d.Name {
	case "sox":
		return "Voice input"
	case "ffmpeg":
		return "Camera vision"
	default:
		return d.Name
	}
}

// offerSpeechSetup runs the speech wizard, but only after asking — it is the
// longest stage, it downloads models, and a user who only wants a text shell
// should not have to sit through it to reach a prompt.
func offerSpeechSetup() {
	fmt.Println()
	fmt.Println(shell.PanelTitle("voice"))
	fmt.Println(shell.PanelLine(shell.Muted("Helix can listen and speak, locally or through a cloud provider")))
	fmt.Println(shell.PanelLine(shell.Muted("this picks the STT/TTS chain and verifies it actually answers")))
	fmt.Println(shell.PanelEnd())
	if !commands.AskForConfirmation("Set up voice now? (you can do this later with /blackbox setup)") {
		uiIdle("skipped", "run /blackbox setup when you want it")
		return
	}
	handleVoiceSetup()
}

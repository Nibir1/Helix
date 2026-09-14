// internal/agent/file.go
// Purpose: the `file` tool step — read, list, glob, grep, edit, write.
//
// WHY THE PLANNER NEEDED THIS. Until now the only way for a plan to touch a
// file was `shell`, and the planner prompt said so in as many words: "For
// in-place file editing on macOS, use: sed -i ” 's/OLD/NEW/g' FILE". That is a
// bad instruction to give a model. An in-place sed whose pattern does not match
// exits 0 and changes nothing — so the model is told the edit succeeded when
// the file is untouched, reports the work as done, and the next step builds on
// a change that was never made. It cannot distinguish one occurrence from six.
// Quoting real code through a shell line means escaping it twice.
//
// The implementation is ported from Synapse's agentloop, whose tool semantics
// are the right ones. What is Helix's, and is NOT optional, is everything in
// this file: the tier, the posture, the voice cap, the sandbox and the hooks. A
// capability arrives through the same gates as every other capability, or the
// gates are decoration.
//
// THE TIERS, and why these and not others:
//
//	read, list, glob, grep  -> Low.    They change nothing. Under the default
//	                                   posture they run; under `cautious` they
//	                                   still ask, because "confirm everything"
//	                                   has to mean it.
//	edit, write             -> Medium. They change the machine. Under the
//	                                   default posture they ask, exactly as a
//	                                   medium-risk shell command does.
//
// Nothing here is High, so nothing here is blocked outright — and that is a
// deliberate limit worth stating: a `write` confined to the sandbox root cannot
// reach /etc, and the paths that WOULD be high-risk are refused by the sandbox
// before a tier is ever consulted.
package agent

import (
	"fmt"
	"os"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/filetools"
	"helix/internal/hooks"
)

// fileMutates reports whether an action changes the filesystem. One predicate,
// consulted by the tier, the announcement and the dry-run check, so the three
// cannot disagree about what counts as a write.
func fileMutates(action string) bool {
	return action == "edit" || action == "write"
}

// handleFileStep runs one validated file step through the same gates a shell
// step passes, and returns what the tool produced for the execution report.
func (a *Agent) handleFileStep(step ai.PlanStep) (string, error) {
	action := strings.TrimSpace(step.Action)
	path := strings.TrimSpace(step.Args["path"])

	// The subject is what the user sees, what a hook matches on, and what the
	// plan-mode line prints — one rendering so all three agree.
	subject := fileSubject(action, step.Args)

	risk := commands.ShellRiskLow
	if fileMutates(action) {
		risk = commands.ShellRiskMedium
	}

	// The Voice Risk Policy caps voice-originated plans at medium. Nothing here
	// is high, so the cap never fires today — it is called anyway because the
	// day someone adds a high-risk file action, the call site that forgot to
	// ask is the hole, not the action.
	// The reasons are what the confirmation prompt shows. "file edit" alone is
	// the subject restated; what a user deciding whether to approve needs is
	// which file and what kind of change, so the reason names the effect.
	var reasons []string
	if fileMutates(action) {
		reasons = append(reasons, fileChangeReason(action, step.Args))
	}
	var voiceBlocked bool
	risk, reasons, voiceBlocked = voiceCapRisk(risk, reasons, a.voiceActive())

	mode := a.Permission()
	if mode == PermissionPlan {
		a.render.PrintWarning("[plan] would " + subject)
		a.render.PrintInfo(fmt.Sprintf("       risk: %s — /permissions ask to allow execution", riskName(risk)))
		for _, r := range reasons {
			a.render.PrintInfo("       • " + r)
		}
		return "", nil
	}

	switch risk {
	case commands.ShellRiskLow:
		if mode == PermissionCautious && !commands.AskForConfirmation("Run "+subject+"?") {
			a.render.PrintWarning("File step skipped")
			return "", nil
		}
	case commands.ShellRiskMedium:
		switch {
		case step.Trusted:
			a.render.PrintDebug("File change auto-confirmed (trusted local source)")
		case mode == PermissionAuto:
			a.render.PrintWarning("File change auto-approved (/permissions auto): " + subject)
			for _, r := range reasons {
				a.render.PrintWarning("   • " + r)
			}
		default:
			a.render.PrintWarning("This will change a file: " + subject)
			for _, r := range reasons {
				a.render.PrintWarning("   • " + r)
			}
			if !commands.AskForConfirmation("Apply this change?") {
				a.render.PrintWarning("File step skipped")
				return "", nil
			}
		}
	case commands.ShellRiskHigh:
		a.render.PrintError("HIGH RISK — blocked")
		if voiceBlocked {
			a.speak("That file operation is too dangerous to run by voice. Please use the terminal.")
		}
		return "", fmt.Errorf("high-risk file step blocked")
	}

	// A dry run stops here, after the gates and before the effect — but only
	// for the actions that HAVE an effect. Refusing to let /dry-run read a file
	// would make the mode useless for inspecting what a plan is about to do.
	if a.execConfig.DryRun && fileMutates(action) {
		a.render.PrintWarning("[Dry Run] Would " + subject)
		return "", nil
	}

	wd, err := os.Getwd()
	if err != nil || wd == "" {
		wd = a.sandbox.GetCurrentDirectory()
	}

	// Hooks fire LAST, after every built-in gate has approved — so a hook can
	// only ever subtract permission, never grant what the tiers refused.
	hookCtx := hooks.Context{Tool: "file", Action: action, Command: path, Dir: wd}
	if err := a.runPreHooks(hooks.PreFile, hookCtx); err != nil {
		return "", err
	}

	a.render.PrintCommand(subject)
	out, runErr := a.runFileAction(action, step.Args)
	a.runPostHooks(hooks.PostFile, hookCtx, runErr)
	if runErr != nil {
		return "", runErr
	}

	// A mutation's result is a one-line receipt and belongs on screen. A read's
	// result is the file, and printing it would duplicate what already goes to
	// the planner in the execution report — so it is reported, not echoed.
	if fileMutates(action) {
		a.render.PrintSuccess(out)
	} else {
		a.render.PrintDebug(fmt.Sprintf("%s returned %d bytes", action, len(out)))
	}
	return out, nil
}

// runFileAction performs the action. The sandbox is passed as the Resolver, so
// confinement is Helix's existing one — symlinks resolved on both sides, case
// folded for macOS and Windows, a non-existent target validated by its parent —
// rather than a second, weaker copy living in the tool package.
func (a *Agent) runFileAction(action string, args map[string]string) (string, error) {
	r := a.sandbox
	switch action {
	case "read":
		return filetools.Read(r, args["path"])
	case "list":
		return filetools.List(r, args["path"])
	case "glob":
		return filetools.Glob(r, args["pattern"], args["path"])
	case "grep":
		return filetools.Grep(r, args["pattern"], args["path"])
	case "edit":
		return filetools.Edit(r, args["path"], args["old_string"], args["new_string"],
			strings.EqualFold(strings.TrimSpace(args["replace_all"]), "true"))
	case "write":
		return filetools.Write(r, args["path"], args["content"])
	}
	// Unreachable through the planner, which closes the vocabulary before
	// dispatch. Reported rather than ignored so a direct caller cannot get a
	// silent no-op that looks like success.
	return "", fmt.Errorf("unsupported file action: %s", action)
}

// fileChangeReason explains, in one line, what a mutation is about to do.
func fileChangeReason(action string, args map[string]string) string {
	path := args["path"]
	if action == "write" {
		return fmt.Sprintf("replaces the entire contents of %s (%d bytes)", path, len(args["content"]))
	}
	scope := "the first occurrence"
	if strings.EqualFold(strings.TrimSpace(args["replace_all"]), "true") {
		scope = "EVERY occurrence"
	}
	return fmt.Sprintf("replaces %s of a %d-character snippet in %s",
		scope, len(args["old_string"]), path)
}

// fileSubject renders a step as one human-readable line.
func fileSubject(action string, args map[string]string) string {
	switch action {
	case "glob":
		where := args["path"]
		if where == "" || where == "." {
			return "glob " + args["pattern"]
		}
		return "glob " + args["pattern"] + " in " + where
	case "grep":
		where := args["path"]
		if where == "" {
			where = "."
		}
		return "grep " + args["pattern"] + " in " + where
	case "edit":
		return "edit " + args["path"]
	case "write":
		return "write " + args["path"]
	case "list":
		where := args["path"]
		if where == "" {
			where = "."
		}
		return "list " + where
	default:
		return action + " " + args["path"]
	}
}

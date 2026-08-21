// internal/agent/api.go
// Purpose: the exported surface the interactive shell's slash commands drive.
//
// These are thin, deliberate seams. Each one reuses an existing internal path
// rather than reimplementing it, so a command can never become a second,
// weaker copy of the pipeline: /plan reuses the planner and the safety
// preparation, /undo reuses the journal handler that runs reversals through the
// full pipeline, /web reuses the same guarded fetch the planner's web tool uses.
package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/session"
)

// PlanPreview plans a request WITHOUT executing anything, returning the plan
// the pipeline would have run.
//
// It is the read-only half of HandleInput: same planner prompt, same firewall
// canary check, same parse, same safety preparation — and then it stops. The
// critic pass is deliberately skipped: a quarantine exists to prevent
// execution, and there is none here, so running it would only cost a model
// call and hide the plan the user asked to see.
func (a *Agent) PlanPreview(userInput string) (*ai.Plan, error) {
	userInput = strings.TrimSpace(normalizeUserInput(userInput))
	if userInput == "" {
		return nil, fmt.Errorf("nothing to plan")
	}

	ragContext, canary := "", ""
	if a.rag != nil && a.rag.IsInitialized() {
		if cmds, err := a.rag.Retrieve(userInput); err == nil && len(cmds) > 0 {
			ragContext, canary = BuildFirewallContext(cmds)
		}
	}
	ragContext += a.projectContextBlock()
	ragContext += a.sessionContextBlock()
	ragContext += a.todoContextBlock()

	cwd := a.sandbox.GetCurrentDirectory()
	if wd, err := os.Getwd(); err == nil && wd != "" {
		cwd = wd
	}
	envDesc := fmt.Sprintf("OS: %s, Shell: %s, CWD: %s", a.env.OSName, a.env.Shell, cwd)

	think := newThinkerFor(a.render, "HELIX :: PLANNING")
	think.Start()
	raw, err := ai.RunPlannerWithRetry(ai.BuildPlannerPrompt(userInput, envDesc, ragContext))
	think.Stop()
	if err != nil {
		return nil, err
	}

	if canaryEchoed(canary, raw) {
		return nil, fmt.Errorf("injection alert: retrieved-content canary echoed in plan")
	}

	plan, err := ai.ParsePlanFromModelOutput(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}

	// Show the plan as it would actually run — post safety rewrite — so the
	// preview cannot differ from the execution.
	if safePlan, perr := a.prepareSafePlan(userInput, plan); perr == nil && safePlan != nil {
		plan = safePlan
	}
	return plan, nil
}

// RequestUndo offers the most recent reversible action. Exported so /undo is
// reachable by keyboard: the reversal was previously only offered to a spoken
// "undo that", which left typed sessions with a journal they could not use.
func (a *Agent) RequestUndo() { a.handleUndoRequest() }

// WebSearch runs one guarded web search and returns the retrieved text.
func (a *Agent) WebSearch(query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("empty search query")
	}
	ctx, cancel := context.WithTimeout(context.Background(), webTimeout)
	defer cancel()
	return webSearch(ctx, query)
}

// WebFetch retrieves one URL through the same public-address guard the planner's
// web tool uses, so a hand-typed /web cannot reach a private endpoint that the
// planner would have been refused.
func (a *Agent) WebFetch(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("empty URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), webTimeout)
	defer cancel()
	return webFetch(ctx, target)
}

// RenderWeb prints retrieved web text with the same formatting the planner's
// web tool uses.
func (a *Agent) RenderWeb(out string) { a.renderWebResult(out) }

// AskModel runs a one-shot prompt through the active provider and prints the
// answer with the agent's own renderer (typing effect, voice seam, session
// record). Commands that need an AI answer rather than a plan — /review,
// /commit, /explain — go through here so their output looks and behaves like
// every other Helix reply.
func (a *Agent) AskModel(label, prompt string, cfg ai.ModelConfig) (string, error) {
	think := newThinkerFor(a.render, label)
	think.Start()
	out, err := ai.RunModelWithConfig(prompt, cfg)
	think.Stop()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// PrintAnswer renders model text as a Helix reply and records it as this turn's
// response, so session memory and TTS see it like any other answer.
func (a *Agent) PrintAnswer(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	a.render.PrintAIMessage(text, a.typingEffect)
	a.lastResponse = text
	a.speak(text)
}

// Renderer exposes the agent's render target so commands print through the same
// layer as the rest of the pipeline instead of writing to stdout directly.
func (a *Agent) Renderer() Renderer { return a.render }

// SessionTurns returns the conversation memory, oldest first (nil when session
// memory is unavailable). Nil-receiver safe: a command that only reports state
// should degrade to "nothing recorded", not crash a session that failed to
// build an agent.
func (a *Agent) SessionTurns() []session.Turn {
	if a == nil || a.Session == nil {
		return nil
	}
	return a.Session.Recent(a.Session.Len())
}

// RunGitAction executes one planner git step through the normal git path, so a
// command-initiated git action keeps the same confirmations, undo journalling,
// and hooks as one the planner produced.
func (a *Agent) RunGitAction(step ai.PlanStep) error {
	return a.handleGitStep(step)
}

// SetDryRun flips the agent's execution preview. The shell owns the /dry-run
// toggle but the agent holds its own copy of the exec config, so without this
// the toggle announced a mode the pipeline never entered.
//
// The git manager gets its own copy at construction, so it has to be told
// separately or `/dry-run` would still execute git operations for real.
func (a *Agent) SetDryRun(on bool) {
	a.execConfig.DryRun = on
	if a.gitManager != nil {
		a.gitManager.SetDryRun(on)
	}
}

// GitManager exposes the agent's git manager.
//
// There must be exactly one: the shell used to declare its own package-level
// gitManager and never assign it, so `/git` ran against a nil receiver whose
// ExecuteConfig was the zero value — which meant SafeMode was FALSE and the
// pre-execution safety check was skipped for every command `/git` produced.
func (a *Agent) GitManager() *commands.GitManager { return a.gitManager }

// DryRun reports whether execution preview is active.
func (a *Agent) DryRun() bool { return a.execConfig.DryRun }

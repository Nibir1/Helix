// internal/agent/hooks.go
// Purpose: the agent side of user-defined hooks (internal/hooks) — firing them
// at the two moments that matter and reporting what they said.
//
// A pre-hook runs only after every built-in gate has already approved the step,
// so a hook can subtract permission and never add it. A post-hook is
// informational: the action already happened, and a non-zero exit there is
// reported, not escalated into a failure the caller must handle.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"helix/internal/hooks"
)

// hookBudget bounds a whole batch of hooks for one event. Individual hooks have
// their own timeouts; this stops a long list of slow hooks from stalling a turn.
const hookBudget = 2 * time.Minute

// runPreHooks fires the pre-* hooks for a step and returns a non-nil error when
// a blocking hook refused it. The error is the refusal reason, so the caller can
// surface it exactly like any other rejected step.
func (a *Agent) runPreHooks(ev hooks.Event, c hooks.Context) error {
	results := a.fireHooks(ev, c)
	if denied, reason := hooks.Denied(results); denied {
		a.render.PrintError("Blocked by local hook policy: " + reason)
		// Voice callers cannot read the terminal; a silent refusal there strands
		// them exactly the way a silent validation block used to (ADR-005).
		if a.voiceActive() {
			a.speak("A local policy hook blocked that step. The terminal explains why.")
		}
		return fmt.Errorf("blocked by hook: %s", reason)
	}
	return nil
}

// runPostHooks fires the post-* hooks for a completed step. It never returns an
// error: the action already ran, and turning a reporting hook's exit code into
// a step failure would misattribute the outcome.
func (a *Agent) runPostHooks(ev hooks.Event, c hooks.Context, stepErr error) {
	if stepErr != nil {
		c.Err = stepErr.Error()
		c.ExitCode = 1
		if code, ok := exitCodeOf(stepErr); ok {
			c.ExitCode = code
		}
	}
	a.fireHooks(ev, c)
}

// fireHooks runs the matching hooks and prints anything they had to say.
func (a *Agent) fireHooks(ev hooks.Event, c hooks.Context) []hooks.Result {
	if a == nil || a.Hooks == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
	defer cancel()

	results := a.Hooks.Run(ctx, ev, c)
	for _, r := range results {
		switch {
		case r.Denied:
			// The denial itself is reported by runPreHooks, which has the whole
			// batch and can name every objection at once.
		case r.Err != nil:
			a.render.PrintWarning(fmt.Sprintf("hook %q failed (exit %d): %v",
				r.Hook.Name, r.ExitCode, r.Err))
			if r.Output != "" {
				a.render.PrintWarning("  " + r.Output)
			}
		case r.Output != "":
			a.render.PrintDebug(fmt.Sprintf("hook %q: %s", r.Hook.Name, r.Output))
		}
	}
	return results
}

// HookCount reports how many hooks are loaded, for /status and /doctor.
func (a *Agent) HookCount() int {
	if a == nil || a.Hooks == nil {
		return 0
	}
	return len(a.Hooks.Hooks)
}

// exitCodeOf extracts a process exit status from an execution error so a
// post-hook can react to WHY a command failed, not just that it did.
func exitCodeOf(err error) (int, bool) {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), true
	}
	return 0, false
}

// FireSessionHook runs a session lifecycle hook. Exported because the shell owns
// the session's boundaries; the agent owns how a hook is run and reported.
func (a *Agent) FireSessionHook(ev hooks.Event, dir string) {
	a.fireHooks(ev, hooks.Context{Tool: "session", Dir: dir})
}

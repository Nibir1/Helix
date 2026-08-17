// internal/agent/renderer.go
// Purpose: Renderer — the Agent's output seam (BlackBox Phase 4A). The
// Agent no longer renders to the terminal directly: TTYRenderer adapts the
// classic *ux.UX, HeadlessRenderer no-ops for the daemon. The PTY e2e suite
// staying green is the proof that interactive behavior is byte-identical.
package agent

import (
	"helix/internal/ux"
)

// Renderer is every output primitive the Agent uses. Confirmations do NOT
// belong here — they route through commands.Prompter (Phase 2) so voice and
// daemon prompters can intercept them.
type Renderer interface {
	PrintSystemMessage(text string)
	PrintAIMessage(text string, useTypingEffect bool)
	PrintCommand(command string)
	PrintData(data string)
	PrintSuccess(message string)
	PrintError(message string)
	PrintWarning(message string)
	PrintInfo(message string)
	PrintDebug(message string)

	// Interactive reports whether terminal animations (spinners) should
	// run. Headless renderers return false.
	Interactive() bool
}

// TTYRenderer adapts the interactive terminal UX.
type TTYRenderer struct{ UX *ux.UX }

func (r TTYRenderer) PrintSystemMessage(t string)          { r.UX.PrintSystemMessage(t) }
func (r TTYRenderer) PrintAIMessage(t string, typing bool) { r.UX.PrintAIMessage(t, typing) }
func (r TTYRenderer) PrintCommand(c string)                { r.UX.PrintCommand(c) }
func (r TTYRenderer) PrintData(d string)                   { r.UX.PrintData(d) }
func (r TTYRenderer) PrintSuccess(m string)                { r.UX.PrintSuccess(m) }
func (r TTYRenderer) PrintError(m string)                  { r.UX.PrintError(m) }
func (r TTYRenderer) PrintWarning(m string)                { r.UX.PrintWarning(m) }
func (r TTYRenderer) PrintInfo(m string)                   { r.UX.PrintInfo(m) }
func (r TTYRenderer) PrintDebug(m string)                  { r.UX.PrintDebug(m) }
func (r TTYRenderer) Interactive() bool                    { return true }

// HeadlessRenderer swallows output for the daemon path. Debug lines can be
// surfaced for daemon diagnostics; everything else is silent (the journal
// and IPC responses carry the information instead).
type HeadlessRenderer struct {
	// OnDebug, when set, receives debug lines (daemon diagnostics hook).
	OnDebug func(string)
}

func (r HeadlessRenderer) PrintSystemMessage(string)       {}
func (r HeadlessRenderer) PrintAIMessage(t string, _ bool) { r.note(t) }
func (r HeadlessRenderer) PrintCommand(string)             {}
func (r HeadlessRenderer) PrintData(string)                {}
func (r HeadlessRenderer) PrintSuccess(string)             {}
func (r HeadlessRenderer) PrintError(string)               {}
func (r HeadlessRenderer) PrintWarning(string)             {}
func (r HeadlessRenderer) PrintInfo(string)                {}
func (r HeadlessRenderer) PrintDebug(m string)             { r.note(m) }
func (r HeadlessRenderer) Interactive() bool               { return false }

func (r HeadlessRenderer) note(text string) {
	if r.OnDebug != nil && text != "" {
		r.OnDebug(text)
	}
}

// thinkerShim makes spinner usage safe in headless mode without touching
// every Start/Stop site: a nil real thinker is a no-op.
type thinkerShim struct{ real *ux.Thinker }

func (t thinkerShim) Start() {
	if t.real != nil {
		t.real.Start()
	}
}

func (t thinkerShim) Stop() {
	if t.real != nil {
		t.real.Stop()
	}
}

// newThinkerFor returns the animated spinner for interactive renderers and
// a no-op for headless ones.
func newThinkerFor(r Renderer, label string) thinkerShim {
	if r.Interactive() {
		return thinkerShim{real: ux.NewThinker(label)}
	}
	return thinkerShim{}
}

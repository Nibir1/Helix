// internal/agent/policy_voice.go
// Purpose: Voice Risk Policy engine (ADR-005, docs/threat_model_voice.md).
// Voice is an untrusted input channel: transcribed audio becomes text with
// user authority, bypassing the Instruction Firewall's data-only fencing.
// This engine enforces the structural controls:
//
//  1. Voice-originated plans are capped at Medium risk — High is blocked with
//     an explanation (whatever the phrasing).
//  2. Actions whose built-in confirmation is TYPED (git force-push, hard
//     reset, clean worktree, delete main, critical package removal) can never
//     be confirmed by voice — enforced by the VoicePrompter refusing
//     AskTypedConfirmation outright (cmd/helix/voice_prompter.go).
//  3. Voice confirmations fail closed: timeout/silence/unintelligible = no.
//  4. Transcripts below the confidence gate trigger clarification, not
//     execution (0/unknown confidence from a provider is allowed through —
//     only a REPORTED low score gates).
package agent

import (
	"fmt"
	"strings"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/input"
	"helix/internal/session"
)

// VoicePolicy configures ADR-005 enforcement. Defaults in DefaultVoicePolicy.
type VoicePolicy struct {
	// MinTranscriptConfidence gates execution when the STT provider reports
	// a confidence score (0 = provider does not report; never gates).
	MinTranscriptConfidence float64
}

// DefaultVoicePolicy returns the shipped enforcement settings.
func DefaultVoicePolicy() VoicePolicy {
	return VoicePolicy{MinTranscriptConfidence: 0.6}
}

// VoiceDeniedAction documents the deny-by-voice list: actions whose
// confirmation is typed and therefore unreachable from the voice channel.
type VoiceDeniedAction struct {
	Tool           string // planner tool ("git" | "package")
	Action         string // planner action
	RequiredPhrase string // the typed phrase that guards it
	SpokenRefusal  string // what the user hears instead
}

// VoiceDenyList enumerates the actions voice can never confirm (threat V3).
// The authoritative enforcement is the typed-confirmation path itself; this
// list documents and test-covers it (policy table-driven tests).
func VoiceDenyList() []VoiceDeniedAction {
	return []VoiceDeniedAction{
		{Tool: "git", Action: "push --force", RequiredPhrase: "YES, FORCE PUSH",
			SpokenRefusal: "Force push needs typed confirmation. Please type it in the terminal."},
		{Tool: "git", Action: "reset --hard", RequiredPhrase: "YES, RESET HARD",
			SpokenRefusal: "Hard reset needs typed confirmation. Please type it in the terminal."},
		{Tool: "git", Action: "clean", RequiredPhrase: "YES, CLEAN WORKTREE",
			SpokenRefusal: "Cleaning the worktree needs typed confirmation. Please type it in the terminal."},
		{Tool: "git", Action: "delete main branch", RequiredPhrase: "YES, DELETE MAIN",
			SpokenRefusal: "Deleting the main branch needs typed confirmation. Please type it in the terminal."},
	}
}

// HandleInputEvent is the channel-aware entry point: it stamps the input
// channel + metadata for the Voice Risk Policy, applies the transcript
// confidence gate, then delegates to the standard HandleInput pipeline —
// classify → plan → firewall → risk tiers → sandbox all still apply.
// With Phase 4B statefulness it also serves "undo that" from the undo
// journal and records the turn into session memory.
func (a *Agent) HandleInputEvent(ev input.InputEvent) {
	a.channel = input.ChannelText
	a.turnMeta = ev.Meta
	a.lastResponse = ""
	a.turnWasControl = false
	defer func() {
		a.channel = input.ChannelText
		a.turnMeta = nil
		a.recordTurn(ev)
	}()

	if ev.Channel != "" && ev.Channel.Valid() {
		a.channel = ev.Channel
	}

	if a.channel == input.ChannelVoice {
		policy := DefaultVoicePolicy()
		if conf, ok := numericMeta(ev.Meta, "stt_confidence"); ok && conf > 0 &&
			conf < policy.MinTranscriptConfidence {
			a.speak("I did not catch that clearly. Could you repeat it?")
			a.render.PrintWarning(fmt.Sprintf(
				"[voice policy] transcript confidence %.2f below gate %.2f — asking to repeat",
				conf, policy.MinTranscriptConfidence))
			return
		}
	}

	// Safe-subset undo (roadmap 4B): a bare undo intent is answered from
	// the journal; the reversal runs through the FULL safety pipeline.
	if isUndoIntent(ev.Text) {
		a.handleUndoRequest()
		return
	}

	// BlackBox Phase 5: voice + eyes-on + deictic → capture one frame and
	// answer through the vision model (memory-only).
	if a.visionRequested(ev) {
		a.handleVisionTurn(ev)
		return
	}

	a.HandleInput(ev.Text)
}

// isUndoIntent matches bare undo utterances ("undo", "undo that", "undo the
// last thing"). Anything more specific routes to the planner normally.
func isUndoIntent(text string) bool {
	t := strings.TrimRight(strings.TrimSpace(strings.ToLower(text)), ".!?")
	switch t {
	case "undo", "undo that", "undo this", "undo it", "undo the last thing", "undo the last command":
		return true
	}
	return false
}

// handleUndoRequest offers the latest reversible action. The reversal is a
// normal shell step: validation, risk tiers, sandbox, and the Voice Risk
// Policy all apply — an undo must never be an execution bypass.
func (a *Agent) handleUndoRequest() {
	if a.Undo == nil {
		a.render.PrintInfo("Undo journal not available in this session.")
		return
	}
	entry, ok, err := a.Undo.Last()
	if err != nil {
		a.render.PrintError(fmt.Sprintf("Undo journal read failed: %v", err))
		return
	}
	if !ok {
		a.render.PrintInfo("Nothing reversible on the undo journal yet (commits are journalled).")
		return
	}

	a.render.PrintWarning(fmt.Sprintf(
		"Undo %q (%s)? Reversal: %s", entry.Description, entry.Timestamp.Format("15:04:05"), entry.ReversalCmd))
	if !commands.AskForConfirmation("Run the reversal?") {
		a.render.PrintWarning("Undo skipped")
		return
	}

	step := ai.PlanStep{Tool: "shell", Command: entry.ReversalCmd}
	if err := a.handleShellStep(step); err != nil {
		a.render.PrintError(fmt.Sprintf("Undo failed: %v", err))
		return
	}
	// Consume the entry only after a successful reversal: a reversal may run
	// once. Without this, "undo that" twice reruns `git reset --soft HEAD~1`
	// and rewinds a commit that was never journalled as reversible.
	if _, _, perr := a.Undo.Pop(); perr != nil {
		a.render.PrintDebug(fmt.Sprintf("undo journal pop: %v", perr))
	}
	a.render.PrintSuccess(fmt.Sprintf("Undone: %s", entry.Description))
	a.speak("Undone.")
}

// recordTurn stores the exchange in session memory (nil-safe).
//
// Slash commands are deliberately excluded: they are control input, not part of
// the conversation. Including them polluted the planner's session context with
// lines the user never said to the model, and left /clear unable to clear
// itself.
func (a *Agent) recordTurn(ev input.InputEvent) {
	if a.Session == nil || a.turnWasControl {
		return
	}
	a.Session.Append(session.Turn{
		Channel:  string(ev.Channel),
		UserText: ev.Text,
		Reply:    a.lastResponse,
	})
}

// voiceActive reports whether the current turn originated from speech.
func (a *Agent) voiceActive() bool { return a.channel == input.ChannelVoice }

// voiceCapRisk enforces the Medium-risk ceiling for the voice channel.
// Returns (cappedRisk, blocked, explanation): blocked=true means the step
// must not execute at all.
func voiceCapRisk(risk commands.ShellRiskLevel, reasons []string, voice bool) (commands.ShellRiskLevel, []string, bool) {
	if !voice || risk != commands.ShellRiskHigh {
		return risk, reasons, false
	}
	reasons = append(reasons, "voice risk policy: high-risk commands are unreachable from the voice channel (ADR-005)")
	return commands.ShellRiskHigh, reasons, true
}

// speak vocalizes through the OnSpeak seam (wired to TTS in main).
func (a *Agent) speak(text string) {
	if a.OnSpeak != nil {
		a.OnSpeak(text)
	}
}

// numericMeta extracts a float from advisory turn metadata.
func numericMeta(meta map[string]any, key string) (float64, bool) {
	if meta == nil {
		return 0, false
	}
	switch v := meta[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	default:
		return 0, false
	}
}

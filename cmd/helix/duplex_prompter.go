// cmd/helix/duplex_prompter.go
// Purpose: confirmations inside a full-duplex session, where the microphone is
// already owned by somebody else.
//
// WHY VoicePrompter CANNOT BE REUSED HERE. Its Listen opens a recorder
// (speech.RecordClip) and the duplex session's capture loop is already holding
// the only one — a second `rec` gets an error or, worse on some hosts, silence.
// So the answer to a spoken question in a duplex session is not a new recording
// at all: it is simply THE NEXT TURN, which the session was going to deliver
// anyway. That is the whole adaptation.
//
// WHAT DOES NOT CHANGE, because ADR-005 is not what is being adapted:
//
//	rule 2  A typed confirmation is still TYPED. duplexPrompter does not
//	        approve one by voice; it makes typing one possible, which it was
//	        not before — the keyboard was live but nothing stopped the model
//	        talking over someone reading a prompt, and nothing stopped the ROOM
//	        answering it.
//	rule 3  Silence declines. Every path below fails closed on timeout.
//
// THE MUTE IS THE POINT, and it is server-side rather than a local mic gate:
// session.input_audio.mute was verified to stop transcription AND delegation
// outright, so while a destructive prompt is on screen the model cannot hear a
// television — or a person in the room — say the confirmation phrase. Without
// it, "the keyboard is live during a session" would be true and still unsafe.
package main

import (
	"fmt"
	"strings"

	"helix/internal/commands"
	"helix/internal/shell"
)

// duplexPrompter answers questions through the open live session.
type duplexPrompter struct {
	// typed is the fallback for anything that must be typed. The TTY prompter,
	// captured at startup.
	typed commands.Prompter
}

// newDuplexPrompter builds the prompter over the current TTY prompter.
func newDuplexPrompter() *duplexPrompter { return &duplexPrompter{typed: ttyPrompter} }

// AskYesNo asks through the model and reads the next spoken turn as the answer.
//
// Anything that is not a recognised yes — silence, a timeout, "maybe", the
// session dying — declines, which is ADR-005 rule 3 unchanged. One clarifying
// round, the same as VoicePrompter, because a single misheard word should not
// silently cancel the user's request.
func (p *duplexPrompter) AskYesNo(question string) bool {
	d := duplexCur.Load()
	if d == nil {
		return p.fallback().AskYesNo(question)
	}
	printPrompt(question)
	answer, ok := d.duplexAwaitAnswer(question)
	if !ok {
		return false
	}
	switch classifyYesNo(answer) {
	case yesAnswer:
		return true
	case noAnswer:
		return false
	}
	retry, ok := d.duplexAwaitAnswer("Please answer yes or no.")
	return ok && classifyYesNo(retry) == yesAnswer
}

// AskLine asks through the model and returns the next spoken turn verbatim.
func (p *duplexPrompter) AskLine(prompt string) string {
	d := duplexCur.Load()
	if d == nil {
		return p.fallback().AskLine(prompt)
	}
	printPrompt(prompt)
	answer, ok := d.duplexAwaitAnswer(prompt)
	if !ok {
		return ""
	}
	return strings.TrimSpace(answer)
}

// AskTypedConfirmation mutes the session and hands the question to the
// keyboard.
//
// This is the one genuinely new capability: before, a destructive action inside
// a voice conversation was simply unreachable, because VoicePrompter refuses
// every typed confirmation and nothing coordinated the handover. Now the model
// is told to stop listening first, the phrase is typed into a terminal nobody
// is talking over, and listening resumes afterwards — whatever the answer was,
// and even if the read panicked its way out, because the unmute is deferred.
//
// Fails CLOSED in the one case that matters: if the mute is REFUSED, the
// confirmation is refused too. A typed confirmation taken while the room can
// still be heard is exactly the door ADR-005 rule 2 exists to keep shut.
func (p *duplexPrompter) AskTypedConfirmation(label, requiredPhrase string) bool {
	d := duplexCur.Load()
	if d == nil {
		return p.fallback().AskTypedConfirmation(label, requiredPhrase)
	}
	return askTypedUnderMute(d.sess.MuteInput, d.sess.UnmuteInput, func() bool {
		fmt.Println(shell.Hint("the microphone is muted while you type this"))
		return p.fallback().AskTypedConfirmation(label, requiredPhrase)
	})
}

// askTypedUnderMute runs a typed confirmation with the live session deafened.
//
// Extracted so the ORDER can be tested without a live session, because the
// order is the whole security property and every part of it is load-bearing:
//
//	mute fails  -> REFUSE. Never ask a question the room can answer.
//	ask panics  -> still unmute. A session left deaf is a microphone that
//	               silently stopped working for the rest of the conversation.
//	unmute fails-> report, but keep the answer. The confirmation itself was
//	               taken correctly; the session is what is now degraded.
func askTypedUnderMute(mute, unmute func() error, ask func() bool) bool {
	if err := mute(); err != nil {
		uiFail("typed confirmation", "could not stop the live session listening: "+err.Error())
		return false
	}
	defer func() {
		if err := unmute(); err != nil {
			uiWarn("live session", "the microphone is still muted: "+err.Error())
		}
	}()
	return ask()
}

// fallback is the typed prompter, never nil.
func (p *duplexPrompter) fallback() commands.Prompter {
	if p.typed != nil {
		return p.typed
	}
	return commands.ActivePrompter()
}

// printPrompt echoes a spoken question, for the same reason VoicePrompter.say
// does: if the voice path is down the user must still SEE the question, or a
// confirmation becomes an invisible auto-decline.
func printPrompt(text string) {
	fmt.Printf("[live] %s\n", shell.Plain(text))
}

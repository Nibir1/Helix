// cmd/helix/voice_prompter.go
// Purpose: VoicePrompter — the commands.Prompter implementation used in voice
// mode (BlackBox Phase 2, ADR-005). Questions are SPOKEN and answered by
// speech; every failure mode fails CLOSED (silence, timeout, or an
// unintelligible answer equals "no" / empty). Typed confirmations are
// structurally impossible from this prompter: it refuses and explains, which
// is what makes the deny-by-voice list (git force push, hard reset, clean,
// delete main) voice-proof (threat V3).
package main

import (
	"context"
	"strings"
	"time"

	"helix/internal/speech"
)

// VoicePrompter implements commands.Prompter for the voice channel.
type VoicePrompter struct {
	// Speak vocalizes a question/prompt. Best-effort: the text is also
	// printed to the terminal so mixed text/voice sessions stay readable.
	Speak func(text string)

	// Listen records and transcribes one utterance within the context.
	Listen func(ctx context.Context) (speech.Transcript, error)

	// Timeout bounds each listen window (fail-closed).
	Timeout time.Duration
}

// NewVoicePrompter builds the prompter with production speak/listen wiring.
func NewVoicePrompter() *VoicePrompter {
	return &VoicePrompter{
		Speak: func(text string) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			// Speech-chain failure is non-fatal: the question was printed.
			_ = speech.Speak(ctx, text)
		},
		Listen: func(ctx context.Context) (speech.Transcript, error) {
			clip, err := speech.RecordClip(ctx, speech.CaptureOptions{MaxDuration: 8 * time.Second})
			if err != nil {
				return speech.Transcript{}, err
			}
			return speech.Transcribe(ctx, clip)
		},
		Timeout: 12 * time.Second,
	}
}

// AskYesNo speaks the question and listens once for a yes/no utterance.
// Anything unrecognized — silence, timeout, "maybe", a sneeze — declines.
func (v *VoicePrompter) AskYesNo(question string) bool {
	v.say(question)

	if transcript, ok := v.listen(); ok {
		switch classifyYesNo(transcript.Text) {
		case yesAnswer:
			return true
		case noAnswer:
			return false
		default:
			// One clarification round, then fail closed.
			v.say("Please answer yes or no.")
			if retry, ok2 := v.listen(); ok2 {
				return classifyYesNo(retry.Text) == yesAnswer
			}
		}
	}
	return false
}

// AskLine speaks the prompt and returns the transcribed reply ("" on failure).
func (v *VoicePrompter) AskLine(prompt string) string {
	v.say(prompt)
	if transcript, ok := v.listen(); ok {
		return strings.TrimSpace(transcript.Text)
	}
	return ""
}

// AskTypedConfirmation is ALWAYS refused by voice (ADR-005 rule 2). The
// refusal is spoken and printed so the user knows to type in the terminal.
func (v *VoicePrompter) AskTypedConfirmation(label, requiredPhrase string) bool {
	refusal := "This operation requires typed confirmation and cannot be approved by voice. " +
		"Please type " + requiredPhrase + " in the terminal."
	v.say(refusal)
	return false
}

func (v *VoicePrompter) say(text string) {
	// Terminal echo keeps mixed-mode sessions coherent.
	speaker := v.Speak
	if speaker == nil {
		return
	}
	speaker(text)
}

func (v *VoicePrompter) listen() (speech.Transcript, bool) {
	listen := v.Listen
	if listen == nil {
		return speech.Transcript{}, false
	}
	timeout := v.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	t, err := listen(ctx)
	if err != nil || strings.TrimSpace(t.Text) == "" {
		return speech.Transcript{}, false
	}
	return t, true
}

type yesNo int

const (
	unknownAnswer yesNo = iota
	yesAnswer
	noAnswer
)

// classifyYesNo maps a spoken reply to yes/no/unknown.
func classifyYesNo(s string) yesNo {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".")
	s = strings.TrimSuffix(s, "!")
	switch s {
	case "yes", "yeah", "yep", "yup", "sure", "ok", "okay", "confirm", "do it", "go ahead", "y":
		return yesAnswer
	case "no", "nope", "nah", "cancel", "stop", "don't", "do not", "n":
		return noAnswer
	default:
		return unknownAnswer
	}
}

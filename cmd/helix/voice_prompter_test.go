// cmd/helix/voice_prompter_test.go
// Purpose: VoicePrompter fail-closed semantics with fake speak/listen —
// no audio hardware (roadmap §9 rule 1).
package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"helix/internal/speech"
)

func newTestVoicePrompter(transcripts ...string) (*VoicePrompter, *[]string) {
	spoken := &[]string{}
	i := 0
	return &VoicePrompter{
		Speak: func(text string) { *spoken = append(*spoken, text) },
		Listen: func(context.Context) (speech.Transcript, error) {
			if i >= len(transcripts) {
				return speech.Transcript{}, errors.New("listen window closed")
			}
			t := transcripts[i]
			i++
			return speech.Transcript{Text: t, Provider: "fake"}, nil
		},
		Timeout: 1,
	}, spoken
}

func TestAskYesNoAccepts(t *testing.T) {
	for _, word := range []string{"yes", "yeah", "ok", "confirm", "Sure."} {
		v, _ := newTestVoicePrompter(word)
		if !v.AskYesNo("Proceed?") {
			t.Errorf("%q must confirm", word)
		}
	}
}

func TestAskYesNoDeclines(t *testing.T) {
	for _, word := range []string{"no", "nope", "cancel", "maybe", "banana"} {
		v, _ := newTestVoicePrompter(word)
		if v.AskYesNo("Proceed?") {
			t.Errorf("%q must not confirm", word)
		}
	}
}

func TestAskYesNoRetryThenFailClosed(t *testing.T) {
	v, spoken := newTestVoicePrompter("hmm", "yes")
	if !v.AskYesNo("Proceed?") {
		t.Error("one clarification round must allow a yes")
	}
	found := false
	for _, s := range *spoken {
		if strings.Contains(s, "yes or no") {
			found = true
		}
	}
	if !found {
		t.Error("clarification must be spoken")
	}

	// Two unknown answers = decline.
	v2, _ := newTestVoicePrompter("hmm", "what")
	if v2.AskYesNo("Proceed?") {
		t.Error("two unintelligible answers must fail closed")
	}
}

func TestAskYesNoListenFailureFailsClosed(t *testing.T) {
	v, _ := newTestVoicePrompter() // no scripted transcripts => listen error
	if v.AskYesNo("Proceed?") {
		t.Fatal("listen failure must decline")
	}
}

func TestAskYesNoSilenceFailsClosed(t *testing.T) {
	v, _ := newTestVoicePrompter("   ") // empty transcript
	if v.AskYesNo("Proceed?") {
		t.Fatal("silence must decline")
	}
}

func TestAskTypedConfirmationAlwaysRefused(t *testing.T) {
	v, spoken := newTestVoicePrompter("YES, FORCE PUSH") // even the exact phrase
	if v.AskTypedConfirmation("force push origin main", "YES, FORCE PUSH") {
		t.Fatal("typed confirmation must NEVER succeed by voice (ADR-005 rule 2)")
	}
	if len(*spoken) == 0 || !strings.Contains((*spoken)[0], "typed confirmation") {
		t.Fatal("refusal must be spoken with guidance")
	}
}

func TestAskLineReturnsTranscript(t *testing.T) {
	v, _ := newTestVoicePrompter("the answer is forty two")
	if got := v.AskLine("Meaning of life?"); got != "the answer is forty two" {
		t.Fatalf("AskLine = %q", got)
	}
}

func TestAskLineFailsClosedOnListenError(t *testing.T) {
	v, _ := newTestVoicePrompter()
	if got := v.AskLine("Prompt"); got != "" {
		t.Fatalf("AskLine must return empty on failure, got %q", got)
	}
}

func TestClassifyYesNo(t *testing.T) {
	cases := map[string]yesNo{
		"yes": yesAnswer, "Yeah.": yesAnswer, "y": yesAnswer,
		"no": noAnswer, "Nope!": noAnswer, "n": noAnswer,
		"": unknownAnswer, "perhaps": unknownAnswer,
	}
	for in, want := range cases {
		if got := classifyYesNo(in); got != want {
			t.Errorf("classifyYesNo(%q) = %v, want %v", in, got, want)
		}
	}
}

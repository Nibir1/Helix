// cmd/helix/voice_silence_test.go
//
// Purpose: staying quiet must never end voice mode.
//
// Reported from a live session: after three silent turns Helix printed
//
//	! not caught  please speak again (attempt 3/3)
//	✘ voice unavailable
//	no speech detected
//
// and dropped to a typed prompt. Silence had a retry budget, and running out of
// it was treated as the microphone having failed.
//
// Two things wrong with that. Being quiet is the ordinary state of a person who
// is not talking, so an assistant that gives up after roughly two minutes of it
// is not listening. And leaving live mode is the user's decision — there are two
// ways to make it, "manual mode" and /blackbox off, and neither was used.
package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"helix/internal/speech"
)

// Silence and an unheard clip are not failures.
func TestSilenceIsNotAFailure(t *testing.T) {
	for _, err := range []error{
		speech.ErrNoSpeech,
		speech.ErrEmptyTranscript,
		fmt.Errorf("wrapped: %w", speech.ErrNoSpeech),
		fmt.Errorf("wrapped: %w", speech.ErrEmptyTranscript),
	} {
		if !silenceIsNotAFailure(err) {
			t.Errorf("%v is treated as a failure; a quiet room would end voice mode", err)
		}
	}
}

// A broken microphone IS a failure, and must still surface. Conflating the two
// in the other direction would strand someone with a dead mic in a loop that
// never says anything.
func TestRealFailuresStillSurface(t *testing.T) {
	for _, err := range []error{
		errors.New("capture: exec: \"sox\": executable file not found in $PATH"),
		errors.New("all STT providers failed"),
		errVoiceHandled,
		errVoiceStopped,
	} {
		if silenceIsNotAFailure(err) {
			t.Errorf("%v is treated as silence; a real fault would never be reported", err)
		}
	}
}

// The loop must have no attempt cap at all.
//
// Checked in the source because the alternative is driving a function that
// records from a microphone. The specific shape matters: an `attempt >= N`
// comparison is what produced the reported behaviour.
func TestVoiceLoopHasNoRetryBudget(t *testing.T) {
	src := readSourceFile(t, "voice_mode.go")
	fn := functionBody(src, "func voiceTurnWithRetry(")
	if fn == "" {
		t.Fatal("voiceTurnWithRetry not found")
	}
	code := stripComments(fn)

	if strings.Contains(code, "maxVoiceRetries") {
		t.Error("the silence path still consults a retry budget; running out of " +
			"one is how voice mode used to end itself")
	}
	for _, shape := range []string{"attempt >=", "attempt >", "attempt ==", "attempt++"} {
		if strings.Contains(code, shape) {
			t.Errorf("the loop counts attempts (%q) — silence has no budget", shape)
		}
	}
	// And silence must reach `continue`, not `return`.
	i := strings.Index(code, "silenceIsNotAFailure(err)")
	if i < 0 {
		t.Fatal("the loop does not distinguish silence from a real failure")
	}
	branch := code[i:]
	if end := strings.Index(branch, "\n\t\t}"); end > 0 {
		branch = branch[:end]
	}
	if !strings.Contains(branch, "continue") {
		t.Error("the silence branch does not continue the loop")
	}
	if strings.Contains(branch, "return") {
		t.Error("the silence branch returns, which hands the caller an error and " +
			"drops the user to a typed prompt")
	}
}

// The constant that bounded silence must be gone, not merely unused.
func TestTheRetryBudgetConstantIsGone(t *testing.T) {
	src := readSourceFile(t, "voice_mode.go")
	if strings.Contains(stripComments(src), "maxVoiceRetries") {
		t.Error("maxVoiceRetries still exists; leaving it invites the next author " +
			"to wire it back in")
	}
}

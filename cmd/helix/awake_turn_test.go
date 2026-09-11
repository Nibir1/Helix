// cmd/helix/awake_turn_test.go
// Purpose: the one behaviour in this change that would be dangerous to get
// wrong and impossible to catch in the PTY suite.
//
// §9 rule 1 keeps audio hardware out of the tests, so "type during a live
// capture" cannot be driven end to end — the harness would have to speak. What
// CAN be pinned is the consequence: a pre-empted turn must cancel the capture
// and submit nothing, because a killed recorder can still hand back a partial
// clip, and a half sentence transcribed from it could read as "manual mode"
// and end the conversation the user was in the middle of.
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"helix/internal/input"
)

// stubAwakeHooks installs fake host interactions for one test.
func stubAwakeHooks(t *testing.T) *struct {
	captureCalls  int
	captureCtxErr error
	restored      int
} {
	t.Helper()
	prev := awakeHooks
	t.Cleanup(func() { awakeHooks = prev })

	state := &struct {
		captureCalls  int
		captureCtxErr error
		restored      int
	}{}

	awakeHooks.keyboardPossible = func() bool { return true }
	awakeHooks.takeTerminal = func() (func(), error) {
		return func() { state.restored++ }, nil
	}
	awakeHooks.keyPending = func() (bool, error) { return false, nil }
	awakeHooks.keyReady = func(time.Duration) (bool, error) { return true, nil }
	awakeHooks.capture = func(ctx context.Context) (input.InputEvent, error) {
		state.captureCalls++
		<-ctx.Done() // a recorder that only stops when cancelled
		state.captureCtxErr = ctx.Err()
		// The dangerous shape: a killed recorder that flushed enough bytes
		// returns a clip with NO error, so a naive caller would submit this.
		return input.InputEvent{Text: "manual mo", Channel: input.ChannelVoice}, nil
	}
	return state
}

func TestKeyboardPreemptSubmitsNothing(t *testing.T) {
	state := stubAwakeHooks(t)

	ev, err := awakeTurn()

	if !errors.Is(err, errKeyboardPreempted) {
		t.Fatalf("err = %v, want errKeyboardPreempted", err)
	}
	if ev.Text != "" {
		t.Errorf("a pre-empted turn produced the event %q — a partial transcript "+
			"reached the planner, and this one would have matched a stop phrase", ev.Text)
	}
	if state.captureCtxErr == nil {
		t.Error("the capture context was never cancelled, so the recorder kept running " +
			"while the user typed")
	}
	if state.restored == 0 {
		t.Error("the terminal was not restored — no echo and no line editing, which to a " +
			"user is indistinguishable from a hang")
	}
}

// Already typing when the turn begins: no recorder, no chime. Opening the
// microphone here would beep over someone mid-word and record them finishing.
func TestAlreadyTypingSkipsTheCaptureEntirely(t *testing.T) {
	state := stubAwakeHooks(t)
	awakeHooks.keyPending = func() (bool, error) { return true, nil }

	ev, err := awakeTurn()
	if !errors.Is(err, errKeyboardPreempted) {
		t.Fatalf("err = %v, want errKeyboardPreempted", err)
	}
	if ev.Text != "" {
		t.Errorf("produced an event %q without capturing", ev.Text)
	}
	if state.captureCalls != 0 {
		t.Errorf("opened the microphone %d times over someone who was already typing",
			state.captureCalls)
	}
}

// A spoken turn that finishes before any keystroke must come through
// untouched, or the pre-empt machinery has eaten the feature it protects.
func TestUninterruptedTurnStillSpeaks(t *testing.T) {
	state := stubAwakeHooks(t)
	awakeHooks.keyReady = func(d time.Duration) (bool, error) {
		time.Sleep(d) // never ready
		return false, nil
	}
	awakeHooks.capture = func(context.Context) (input.InputEvent, error) {
		state.captureCalls++
		return input.InputEvent{Text: "what is the time", Channel: input.ChannelVoice}, nil
	}

	ev, err := awakeTurn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Text != "what is the time" {
		t.Errorf("transcript = %q, want the spoken line", ev.Text)
	}
	if ev.Channel != input.ChannelVoice {
		t.Errorf("channel = %v, want voice — provenance decides the prompter and the "+
			"typed-only command guards", ev.Channel)
	}
	if state.restored == 0 {
		t.Error("the terminal was not restored after a normal turn")
	}
}

// The degraded path: no cbreak, no watcher. Bytes typed during a capture there
// were never watched and were never going to be acted on, so they must not
// arrive as the first half of the next typed line.
func TestVoiceOnlyTurnStillTakesATurn(t *testing.T) {
	state := stubAwakeHooks(t)
	awakeHooks.keyboardPossible = func() bool { return false }
	awakeHooks.capture = func(context.Context) (input.InputEvent, error) {
		state.captureCalls++
		return input.InputEvent{Text: "hello", Channel: input.ChannelVoice}, nil
	}

	ev, err := awakeTurn()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.Text != "hello" {
		t.Errorf("transcript = %q", ev.Text)
	}
	if state.restored != 0 {
		t.Error("the voice-only path took the terminal, which it must not")
	}
}

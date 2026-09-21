// cmd/helix/awake_turn.go
// Purpose: take one turn in AWAKE, with the keyboard live for its whole
// duration.
//
// THE PROBLEM. "when I use any wake phrase, it wakes up and listens. but if I
// try to type, it doesn't allow to type." Both halves were true. In a
// conversation nothing read stdin at all, and the terminal was left in the
// canonical, echoing mode it was inherited in — so keystrokes were printed by
// the kernel into the middle of the animated HUD line and then sat in the
// terminal's queue until something happened to read a line. Typing looked
// broken because it was.
//
// THE SHAPE, given that a blocked read cannot be pre-empted. Nothing blocks on
// a read. The terminal is held in cbreak for the turn (see
// internal/shell/cbreak_unix.go — cbreak and not raw, because raw would clear
// ISIG and silently delete Ctrl+C), one goroutine polls stdin at the same
// 60 ms cadence the armed prompt uses, and the first byte to arrive cancels
// the capture and hands the turn to ReadLine. The byte itself is never
// consumed here, so ReadLine reads it as if it had been typed a moment later
// and the line editor is untouched.
//
// WHAT THIS COSTS, stated because it is a real limitation and not a detail:
// once a keystroke wins, the microphone is closed for as long as the line is
// being typed. Speaking mid-line is not heard. That is the same limitation
// keywait_unix.go already documents for the armed prompt, now inherited by
// AWAKE, and lifting it needs a concurrent line editor — the design that was
// measured and rejected.
package main

import (
	"context"
	"os"
	"strings"
	"time"

	"helix/internal/input"
	"helix/internal/shell"
	"helix/internal/utils"
)

// awakeHooks are the host interactions awakeTurn makes.
//
// Indirected because the behaviour that matters most here cannot otherwise be
// tested: "a key was pressed mid-capture, so the partial clip must NOT be
// transcribed or submitted" needs a microphone and a terminal to exercise for
// real, and §9 rule 1 keeps audio hardware out of the suite. With these seams
// the dangerous path — a half sentence reaching the planner, possibly reading
// as "manual mode" and ending the conversation — is asserted in milliseconds.
var awakeHooks = struct {
	keyboardPossible func() bool
	takeTerminal     func() (func(), error)
	keyPending       func() (bool, error)
	keyReady         func(time.Duration) (bool, error)
	capture          func(context.Context) (input.InputEvent, error)
}{
	keyboardPossible: awakeKeyboardPossible,
	takeTerminal:     func() (func(), error) { return shell.MakeCbreak(os.Stdin) },
	keyPending:       shell.KeyPending,
	keyReady:         shell.KeyReady,
	capture:          awakeCapture,
}

// awakeKeyboardPossible reports whether this turn can watch the keyboard.
//
// Three independent reasons it might not: the platform has no cbreak (Windows),
// stdin is not a terminal (a pipeline, every test binary), or the user turned
// it off. The last is why this is a config key rather than a build tag — the
// terminal handling is the riskiest part of this feature, and `awake_keyboard:
// false` is one line away from the old behaviour on a host where it misbehaves.
func awakeKeyboardPossible() bool {
	return shell.CbreakSupported() &&
		shell.KeyWaitSupported() &&
		shell.StdinIsTerminal() &&
		cfg.Speech.WakeWord.AwakeKeyboardLive()
}

// awakeTurn takes one conversational turn. It returns a typed event when the
// keyboard won, a spoken one when the microphone did.
func awakeTurn() (input.InputEvent, error) {
	if !awakeHooks.keyboardPossible() {
		// Voice-only. Bytes typed during a capture here were never watched and
		// were never going to be acted on, so they are stale garbage that
		// would otherwise arrive as the first half of the NEXT typed line.
		// This is the one place a flush is correct.
		ev, err := awakeHooks.capture(context.Background())
		_ = shell.DiscardPendingInput()
		return ev, err
	}

	// Already typing. No chime, no recorder: opening the microphone here would
	// beep over someone who is mid-word and record the room while they finish.
	if pending, err := awakeHooks.keyPending(); err == nil && pending {
		return input.InputEvent{}, errKeyboardPreempted
	}

	restore, err := awakeHooks.takeTerminal()
	if err != nil {
		// Could not take the terminal. Degrade to a voice-only turn rather
		// than to no turn at all.
		return awakeHooks.capture(context.Background())
	}
	// Unconditional, and before anything that could panic: a terminal left in
	// cbreak has no echo and no line editing, which to a user is
	// indistinguishable from a hang.
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
	defer cancel()

	stop := make(chan struct{})
	preempted := make(chan struct{})
	go watchKeyboard(stop, preempted, cancel)
	defer close(stop)

	ev, verr := awakeHooks.capture(ctx)

	// Checked AFTER the turn returns, because the capture may have produced a
	// partial clip on its way down: a killed recorder that flushed at least a
	// header returns a clip with NO ERROR, and transcribing that would burn an
	// STT call on half a sentence — a half sentence that could come back as
	// "manual mode" and end the conversation. batchVoiceTurn guards the
	// transcribe call itself; this guards the event.
	select {
	case <-preempted:
		restore()
		return input.InputEvent{}, errKeyboardPreempted
	default:
	}
	return ev, verr
}

// watchKeyboard cancels the turn as soon as stdin has a byte waiting.
//
// Polling rather than reading, for the reason the whole feature rests on: poll
// reports readiness without consuming, so the keystroke is still there for
// ReadLine. The cadence matches shell.DefaultArmedPoll — chosen against human
// latency, not machine cost, since the syscall sleeps.
func watchKeyboard(stop <-chan struct{}, preempted chan<- struct{}, cancel context.CancelFunc) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		ready, err := awakeHooks.keyReady(shell.DefaultArmedPoll)
		if err != nil {
			return // stdin is unusable; the turn runs voice-only
		}
		if !ready {
			continue
		}
		close(preempted)
		cancel()
		return
	}
}

// typedTurn reads one line from the keyboard.
//
// Shared by MANUAL, by STANDBY's keyboard branch and by the keyboard-won path
// out of AWAKE, so a typed line is read the same way whichever state it
// arrived in — and the terminal is in whatever ReadLine expects to find,
// because AWAKE restores cbreak before calling this.
func typedTurn(history []string) (input.InputEvent, error) {
	noteVoiceActivity(time.Now())
	line, err := shell.ReadLine(shell.GetContext(), highlighter, history)
	if err != nil {
		return input.InputEvent{}, err
	}
	return input.InputEvent{Text: strings.TrimSpace(line), Channel: input.ChannelText}, nil
}

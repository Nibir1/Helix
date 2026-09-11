// internal/shell/armedwait.go
// Purpose: wait at an idle prompt for whichever comes first — a keystroke, or
// something else the caller is listening for (a wake word).
//
// Terminal handling lives here rather than in the caller because the mode is
// load-bearing and easy to get wrong. In CANONICAL mode the line discipline
// buffers input until Enter, so `poll(2)` reports nothing readable however many
// characters have been typed — the wait would appear to work and would only
// notice the keyboard once a whole line was submitted. Raw mode is therefore
// not a detail of how this is implemented, it is the reason it can be
// implemented at all. Raw mode also turns echo off, which is what stops the
// first keystroke appearing twice: once from the terminal while we are still
// waiting, and once from the editor when it redraws the line.
//
// The pending byte is never consumed. It stays in the terminal's input queue
// and ReadLine reads it exactly as if it had been typed a moment later, which
// is what keeps the editor untouched by this feature.
package shell

import (
	"os"
	"time"

	"golang.org/x/term"
)

// ArmedResult reports how an ArmedWait ended.
type ArmedResult int

const (
	// ArmedKey: input is waiting. The caller should render its prompt and read
	// a line as usual; the keystroke has NOT been consumed.
	ArmedKey ArmedResult = iota

	// ArmedOther: the caller's channel fired first.
	ArmedOther

	// ArmedUnavailable: this platform or this stdin cannot be waited on, and
	// the caller must fall back to an ordinary blocking prompt.
	ArmedUnavailable
)

// DefaultArmedPoll is how often ArmedWait looks up from the keyboard to check
// the caller's channel.
//
// 60 ms is chosen against human latency, not machine cost: the poll is a
// syscall that sleeps, so the loop is free, and a wake word already takes a
// chunk of audio (1.5 s by default) to be detected. Anything under ~100 ms is
// indistinguishable from instant to the person typing.
const DefaultArmedPoll = 60 * time.Millisecond

// StdinIsTerminal reports whether stdin is a terminal.
//
// Exported because two features now need the same precondition — the armed
// prompt and the keyboard watch inside a conversation — and asking it twice in
// two packages is how the two would come to disagree. Everything in this file
// and cbreak_unix.go is unavailable without it: termios ioctls fail on a pipe,
// and poll(2) on a non-tty answers a different question.
func StdinIsTerminal() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// ArmedWait blocks until stdin has input or `other` receives.
//
// Args:
//   - other: the caller's competing event. A nil channel is legal and makes
//     this a plain "wait for a keystroke".
//   - poll: how often to alternate between the two. Zero uses DefaultArmedPoll.
//
// Returns ArmedUnavailable (with the reason) when stdin is not a terminal or
// the platform has no readiness call — never an error the caller must guess at.
func ArmedWait(other <-chan struct{}, poll time.Duration) (ArmedResult, error) {
	if poll <= 0 {
		poll = DefaultArmedPoll
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return ArmedUnavailable, nil
	}
	if !KeyWaitSupported() {
		return ArmedUnavailable, ErrKeyWaitUnsupported
	}

	// Raw for the duration of the wait, restored before returning — see the
	// file comment. ReadLine sets its own raw mode and restores to whatever it
	// found, so it must find the terminal as it was.
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return ArmedUnavailable, err
	}
	defer func() { _ = term.Restore(fd, oldState) }()

	for {
		// The channel is checked first and without blocking, so an event that
		// arrived during the last poll is not made to wait another round.
		select {
		case <-other:
			return ArmedOther, nil
		default:
		}

		ready, err := KeyReady(poll)
		if err != nil {
			return ArmedUnavailable, err
		}
		if ready {
			return ArmedKey, nil
		}
	}
}

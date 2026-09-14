// internal/ux/lineownership_test.go
// Purpose: the animated HUD must not paint over a reply.
//
// A real duplex session produced this, and it is the whole reason the file
// exists:
//
//	┏━ HELIX ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ deepseek / deepseek-flash ━
//	● ◈ HELIX SPEAKING ╢▄▁▁▃▄▄▃▄▇▇▅▃▃▄▄▂╟ 2.6s outstanding.
//
// The band header survived because it ends in a newline. Everything after it
// did not: the SPEAKING HUD repaints "\r\033[2K" every 100ms, so a paragraph
// streaming into that same line was erased ten times a second and only the
// fragment written after the final repaint reached the user. The answer was
// generated and spoken, and destroyed on screen.
package ux

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func resetLine(t *testing.T) {
	t.Helper()
	lineSuspends.Store(0)
	terminalLineHeld.Store(false)
	t.Cleanup(func() {
		lineSuspends.Store(0)
		terminalLineHeld.Store(false)
	})
}

// The property in one line: while output holds the line, the animation loop
// skips its frame.
func TestSuspendedLineStopsTheAnimation(t *testing.T) {
	resetLine(t)

	if LineSuspended() {
		t.Fatal("the line starts suspended")
	}
	SuspendLine()
	if !LineSuspended() {
		t.Fatal("SuspendLine did not take the line — the HUD would keep painting " +
			"over the reply, which is the original bug")
	}
	ResumeLine()
	if LineSuspended() {
		t.Fatal("ResumeLine did not release the line; the HUD would never animate again")
	}
}

// Several HUDs can be alive at once — a turn's own and the speaking one — so
// holds nest. An inner release must not resume the line under an outer writer.
func TestSuspensionsNest(t *testing.T) {
	resetLine(t)

	SuspendLine()
	SuspendLine()
	ResumeLine()
	if !LineSuspended() {
		t.Fatal("an inner release resumed the animation while an outer writer still " +
			"owns the line — its text would be painted over")
	}
	ResumeLine()
	if LineSuspended() {
		t.Fatal("the outer release did not free the line")
	}
}

// A stray release must not drive the counter negative: the next SuspendLine
// would then have to be called twice before it took effect, and the reply after
// it would be wiped with no way to explain why.
func TestUnbalancedResumeCannotGoNegative(t *testing.T) {
	resetLine(t)

	ResumeLine()
	ResumeLine()
	SuspendLine()
	if !LineSuspended() {
		t.Fatal("after stray releases, one SuspendLine no longer holds the line")
	}
}

func TestSuspendIsRaceFree(t *testing.T) {
	resetLine(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			SuspendLine()
			ResumeLine()
		}()
	}
	wg.Wait()
	if LineSuspended() {
		t.Fatalf("balanced concurrent holds left the line suspended (count=%d); "+
			"the HUD would never animate again", lineSuspends.Load())
	}
}

// LineHeld and LineSuspended are different questions and must stay so.
// LineHeld asks "is a HUD animating" — background chatter checks it and stays
// quiet. LineSuspended asks "is real output writing" — the HUD checks it and
// stands down. Collapsing them would make the reply suppress itself.
func TestHeldAndSuspendedAreDifferentQuestions(t *testing.T) {
	resetLine(t)

	claimTerminalLine()
	if !LineHeld() {
		t.Fatal("claimTerminalLine did not mark the line held")
	}
	if LineSuspended() {
		t.Error("an animating HUD reports the line as suspended; the reply band would " +
			"treat itself as background chatter and stay quiet")
	}

	SuspendLine()
	if !LineHeld() {
		t.Error("suspending output released the HUD's claim; background writers would " +
			"start printing into the middle of the reply")
	}
	if !LineSuspended() {
		t.Error("output did not take the line")
	}
	ResumeLine()
	releaseTerminalLine()
}

// THE WIRING, written before trusting the helpers. Twice today a correct helper
// sat beside a call site that had stopped using it, and every unit test passed.
// This one drives the real PrintAIMessage and samples whether the line was
// actually held WHILE it wrote.
func TestPrintAIMessageHoldsTheLineWhileItWrites(t *testing.T) {
	resetLine(t)

	u := NewUX()
	u.typingSpeed = time.Millisecond // ~200ms for the text below

	// The sampler MUST be bounded. Without a deadline it spins forever when the
	// hold is never taken — so a broken PrintAIMessage hangs the suite instead
	// of failing it, which is a worse outcome than the bug being tested.
	var mu sync.Mutex
	sawHeld := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if LineSuspended() {
				mu.Lock()
				sawHeld = true
				mu.Unlock()
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	// Long enough that the sampler gets a look in, with the typing effect ON so
	// the animated path is the one under test.
	u.PrintAIMessage(strings.Repeat("the quick brown fox. ", 4), true)
	<-done

	mu.Lock()
	held := sawHeld
	mu.Unlock()
	if !held {
		t.Error("PrintAIMessage never suspended the line — an animated HUD would " +
			"repaint over the reply and wipe it as it streams")
	}
	if LineSuspended() {
		t.Error("PrintAIMessage left the line suspended; the HUD would never animate again")
	}
}

// The streaming path takes the hold on first content and keeps it for the whole
// stream. Per-chunk would let the HUD repaint in the gaps between tokens, which
// is the same bug arriving one token at a time.
func TestStreamWriterHoldsAcrossTheWholeStream(t *testing.T) {
	resetLine(t)

	u := NewUX()
	w := u.StreamAIMessage()

	if LineSuspended() {
		t.Fatal("a stream that has produced nothing already holds the line")
	}
	w.Chunk("first ")
	if !LineSuspended() {
		t.Fatal("the stream did not take the line on first content")
	}
	w.Chunk("second ")
	if !LineSuspended() {
		t.Fatal("the hold was dropped between chunks — the HUD repaints in the gaps")
	}
	w.Close()
	if LineSuspended() {
		t.Error("Close did not release the line")
	}
}

// A stream that never produced content took no hold, so Close must not release
// one it does not own — that would drive the counter negative and leave the
// NEXT reply unprotected.
func TestEmptyStreamReleasesNothing(t *testing.T) {
	resetLine(t)

	SuspendLine() // somebody else owns the line
	u := NewUX()
	w := u.StreamAIMessage()
	w.Chunk("   ") // whitespace only: no content
	w.Close()

	if !LineSuspended() {
		t.Fatal("an empty stream released somebody else's hold on the line")
	}
	ResumeLine()
}

// And the loop must honour it. This is the half that cannot be seen from the
// helpers: a correct SuspendLine with a loop that ignores it changes nothing.
func TestAnimationLoopSkipsFramesWhileSuspended(t *testing.T) {
	resetLine(t)

	v := NewVoiceViz()
	// Force the animating state a TTY would give it; Start is a no-op off-TTY.
	v.mu.Lock()
	v.tty = true
	v.running = true
	v.state = VizSpeaking
	v.stop = make(chan struct{})
	v.mu.Unlock()

	painted := captureStdout(t, func() {
		SuspendLine()
		go v.loop(v.stop)
		time.Sleep(350 * time.Millisecond) // three ticks would have painted
		close(v.stop)
		ResumeLine()
	})
	if strings.Contains(painted, "SPEAKING") {
		t.Errorf("the animation painted while output held the line:\n%q", painted)
	}

	// And it resumes afterwards, or the HUD is dead for the rest of the session.
	v.mu.Lock()
	v.stop = make(chan struct{})
	v.mu.Unlock()
	painted = captureStdout(t, func() {
		go v.loop(v.stop)
		time.Sleep(250 * time.Millisecond)
		close(v.stop)
	})
	if !strings.Contains(painted, "SPEAKING") {
		t.Errorf("the animation never resumed after the hold was released:\n%q", painted)
	}
}

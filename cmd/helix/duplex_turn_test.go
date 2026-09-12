// cmd/helix/duplex_turn_test.go
// Purpose: the three defects a real session found, each reproduced against the
// turn machinery rather than against the source text.
//
// These drive duplexSession's own goroutines with no network, no microphone and
// no live.Session — the parts under test are the transcript assembler, the
// orphan rescue and the speech queue, all of which are pure bookkeeping over a
// mutex and some channels.
package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"helix/internal/ux"
)

// newTestDuplex builds a session with the channels wired and no transport.
func newTestDuplex(t *testing.T) (*duplexSession, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &duplexSession{
		cancel:      cancel,
		turns:       make(chan duplexTurn, 4),
		delegations: make(chan string, 8),
		say:         make(chan string, 16),
	}
	go d.assembleTurns(ctx)
	t.Cleanup(cancel)
	return d, cancel
}

// DEFECT 1, from a real session: "[clear throat Are you still here".
//
// Transcript deltas lag the audio by 1.5-2s, so when the delegation lands the
// tail of the sentence is still arriving. Snapshotting at that instant cut the
// user's words in half AND left the remainder as the prefix of the next turn.
func TestLateTranscriptDeltasDoNotLeakIntoTheNextTurn(t *testing.T) {
	d, _ := newTestDuplex(t)

	d.onHeard("Are you still there", 0)
	d.onDelegation("item_one")
	// The tail arrives AFTER the delegation, exactly as the service sends it.
	time.Sleep(50 * time.Millisecond)
	d.onHeard(" [clear throat]", 0)

	first := waitTurn(t, d, 3*time.Second)
	if !strings.Contains(first.text, "clear throat]") {
		t.Errorf("the turn was cut before its tail arrived: %q", first.text)
	}

	// Now a genuinely new utterance. It must not carry any of the first.
	d.onHeard("Manual mode", 0)
	d.onDelegation("item_two")
	second := waitTurn(t, d, 3*time.Second)
	if strings.Contains(second.text, "clear throat") {
		t.Errorf("the previous turn's tail leaked into this one: %q", second.text)
	}
	if strings.TrimSpace(second.text) != "Manual mode" {
		t.Errorf("second turn = %q, want exactly \"Manual mode\"", second.text)
	}
}

// DEFECT 2, from the same session: "manual mode" stranded under [hearing].
//
// The model decides when a turn ends and sometimes decides never. The stop
// phrase is the way out of an open microphone, so it must not be withholdable
// by a third party.
func TestATurnTheModelNeverDelegatesIsStillTaken(t *testing.T) {
	d, _ := newTestDuplex(t)
	d.mu.Lock()
	d.delegation = "item_previous"
	d.mu.Unlock()

	d.onHeard("Manual mode", 0)
	// No delegation. Ever.

	if _, ok := d.takeOrphanedTurn(); ok {
		t.Fatal("the turn was claimed immediately; a sentence still being spoken " +
			"would be split in half")
	}

	// Age the speech past the bound.
	d.mu.Lock()
	d.lastHeard = time.Now().Add(-duplexOrphanWait - time.Second)
	d.mu.Unlock()

	turn, ok := d.takeOrphanedTurn()
	if !ok {
		t.Fatal("an un-delegated transcript is never claimed, so \"manual mode\" can " +
			"never close the microphone when the model declines to hand the turn over")
	}
	if turn.text != "Manual mode" {
		t.Errorf("orphaned turn = %q", turn.text)
	}
	// The previous delegation is still the one to speak on.
	if turn.delegation != "item_previous" {
		t.Errorf("orphan carries delegation %q, want the previous one", turn.delegation)
	}
	// And it must be consumed, not left to fire again next tick.
	if _, ok := d.takeOrphanedTurn(); ok {
		t.Error("the same orphaned turn was claimed twice")
	}
}

// A delegation that arrives normally must beat the orphan path, or every turn
// would be taken early and split.
func TestTheOrphanPathDoesNotPreemptANormalTurn(t *testing.T) {
	d, _ := newTestDuplex(t)
	d.onHeard("what is in this directory", 0)
	if _, ok := d.takeOrphanedTurn(); ok {
		t.Fatal("fresh speech was claimed as orphaned")
	}
	d.onDelegation("item_one")
	turn := waitTurn(t, d, 3*time.Second)
	if turn.delegation != "item_one" {
		t.Errorf("turn carries %q", turn.delegation)
	}
}

// DEFECT 3, the reported "laggy then stuck": duplexSpeak must not wait.
//
// It used to call waitQuiet on the REPL goroutine — up to 12s per reply, and
// gpt-live-1 keeps talking so the wait kept running its full bound. Nothing
// else runs during that: no turn is read, and the keyboard watcher only exists
// inside awakeHooks.capture.
func TestSpeakingDoesNotBlockTheReplOnTheModelsSpeech(t *testing.T) {
	d, _ := newTestDuplex(t)
	d.mu.Lock()
	d.delegation = "item_one"
	d.mu.Unlock()
	duplexCur.Store(d)
	t.Cleanup(func() { duplexCur.Store(nil) })

	// THE MODEL KEEPS TALKING, which is the condition that actually hung. A
	// single lastSpoke is not enough to reproduce it: the old code waited for a
	// 1.5s gap, and one stale timestamp produces that gap in 1.5s. gpt-live-1 is
	// conversational, so the gap kept not arriving and the wait ran its full
	// 12s bound — on every reply. The first version of this test set the
	// timestamp once and passed against the broken code.
	stopTalking := make(chan struct{})
	defer close(stopTalking)
	go func() {
		for {
			select {
			case <-stopTalking:
				return
			default:
			}
			d.mu.Lock()
			d.lastSpoke = time.Now()
			d.mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		duplexSpeak("the answer is 4")
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("duplexSpeak blocked while the model was talking — this is the reported " +
			"lag, and with a conversational model it runs the full 12s bound on every reply")
	}

	// The reply must actually be queued, not dropped.
	select {
	case got := <-d.say:
		if got != "the answer is 4" {
			t.Errorf("queued %q", got)
		}
	default:
		t.Error("the reply was neither spoken nor queued")
	}
}

// A full speech queue must not block either — that would reintroduce the hang
// through the back door.
func TestAFullSpeechQueueStillDoesNotBlock(t *testing.T) {
	d, _ := newTestDuplex(t)
	d.mu.Lock()
	d.delegation = "item_one"
	d.lastSpoke = time.Now()
	d.mu.Unlock()
	duplexCur.Store(d)
	t.Cleanup(func() { duplexCur.Store(nil) })

	for range cap(d.say) + 4 {
		done := make(chan struct{})
		go func() { defer close(done); duplexSpeak("filler") }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("duplexSpeak blocked on a full queue")
		}
	}
}

func waitTurn(t *testing.T, d *duplexSession, within time.Duration) duplexTurn {
	t.Helper()
	select {
	case turn := <-d.turns:
		return turn
	case <-time.After(within):
		t.Fatal("no turn was assembled")
		return duplexTurn{}
	}
}

// Every capture path in Helix shows a waveform while the microphone is hot.
// The duplex turn was written as a third capture path and got none, which on
// screen is a bare blinking cursor under a banner that says "listening".
func TestTheDuplexTurnShowsTheWaveform(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"), "func duplexCapture("))
	if !strings.Contains(body, "ux.VizListening") {
		t.Error("the duplex turn starts no waveform; the screen shows nothing while the " +
			"microphone is open")
	}
	if !strings.Contains(body, "defer d.stopViz()") {
		t.Error("the waveform is not torn down when the turn returns, so it would animate " +
			"over the agent's own output")
	}
	// Metered from the real microphone, not animated regardless.
	pump := stripLineComments(functionBody(readSourceFile(t, "duplex.go"),
		"func (d *duplexSession) pumpMicrophone("))
	if !strings.Contains(pump, "SetLevel(") {
		t.Error("the waveform is never fed a level, so it animates whether or not anything " +
			"is being heard — the defect P12.4 replaced the synthetic animation to fix")
	}
}

// The HUD and the interim transcript share one row. The waveform must let go
// the moment there are words, or the two overwrite each other.
func TestTheWaveformYieldsTheLineToTheTranscript(t *testing.T) {
	body := stripLineComments(functionBody(readSourceFile(t, "duplex.go"),
		"func (d *duplexSession) onHeard("))
	stop := strings.Index(body, "d.stopViz()")
	print := strings.Index(body, "[hearing]")
	if stop < 0 {
		t.Fatal("onHeard does not stop the waveform before printing the transcript")
	}
	if print >= 0 && stop > print {
		t.Error("the transcript is printed before the waveform lets go of the line; they " +
			"would overwrite each other")
	}
}

// stopViz is called from two places for the same turn. The second must be a
// no-op rather than acting on a HUD that is already gone.
func TestStoppingTheWaveformTwiceIsSafe(t *testing.T) {
	d, _ := newTestDuplex(t)
	d.stopViz() // never started
	viz := ux.NewVoiceViz()
	d.viz.Store(viz)
	d.stopViz()
	d.stopViz()
	if d.viz.Load() != nil {
		t.Error("the HUD pointer survived stopViz, so the metering goroutine keeps writing " +
			"to a stopped viz")
	}
}

// The HUD is fed by pumpMicrophone and torn down by onHeard — two different
// goroutines, neither of which is the one that created it. Run under -race.
func TestTheWaveformSurvivesConcurrentMeteringAndTeardown(t *testing.T) {
	d, _ := newTestDuplex(t)
	viz := ux.NewVoiceViz()
	d.viz.Store(viz)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // the capture pump, metering
		defer wg.Done()
		for range 200 {
			if v := d.viz.Load(); v != nil {
				v.SetLevel(0.4)
			}
		}
	}()
	go func() { // the data channel, handing the line to the transcript
		defer wg.Done()
		for range 20 {
			d.stopViz()
		}
	}()
	wg.Wait()
	if d.viz.Load() != nil {
		t.Error("the HUD pointer survived; the pump would keep metering a stopped viz")
	}
}

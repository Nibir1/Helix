// internal/speech/bargein_test.go
// Purpose: barge-in is the one feature here that can make things WORSE if it
// misfires — a false trigger silences Helix mid-reply with no signal that it
// was wrong — so its defaults and its refusal paths are pinned.
package speech

import (
	"context"
	"testing"
	"time"
)

// Off unless asked. A probe that ran by default would sample the microphone in
// every inter-sentence gap of every reply, which is both a latency cost and a
// privacy surprise nobody opted into.
func TestBargeInIsOffByDefault(t *testing.T) {
	if BargeInEnabled() {
		t.Fatal("barge-in must be off until explicitly enabled")
	}
	if BargeInProbe(context.Background()) {
		t.Error("a disabled probe must never report an interruption")
	}
}

// Disabled must also mean FREE: the probe returns before its settle delay, so
// leaving it off costs nothing per sentence.
func TestDisabledBargeInAddsNoLatency(t *testing.T) {
	EnableBargeIn(false)
	start := time.Now()
	_ = BargeInProbe(context.Background())
	if elapsed := time.Since(start); elapsed > bargeSettleDelay {
		t.Errorf("a disabled probe waited %v; it must return immediately", elapsed)
	}
}

// A cancelled reply must not be re-interrupted, and a probe that cannot listen
// must not manufacture an interrupt out of its own failure.
func TestBargeInFailsSafe(t *testing.T) {
	EnableBargeIn(true)
	t.Cleanup(func() { EnableBargeIn(false) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if BargeInProbe(ctx) {
		t.Error("a cancelled context must not report an interruption")
	}
}

// The probe's floor must sit well above the ordinary speech gate. It runs with
// no transcription behind it, so unlike a captured turn there is no second
// check to catch a mistake — the cost of a false positive is Helix silencing
// itself for a chair scrape.
func TestBargeInThresholdIsStricterThanTheSpeechGate(t *testing.T) {
	if bargeRMSFloor <= speechRMSFloor {
		t.Errorf("barge-in floor %v must be stricter than the speech floor %v",
			bargeRMSFloor, speechRMSFloor)
	}
}

// The added gap must stay inside the pause a listener already expects between
// sentences, or interruption is bought with a stutter in every reply.
func TestBargeInGapStaysWithinNaturalSentencePause(t *testing.T) {
	total := bargeSettleDelay + bargeWindow
	if total > 500*time.Millisecond {
		t.Errorf("inter-sentence probe costs %v; ordinary speech pauses ~300-500ms "+
			"between sentences, and past that the gap reads as a stall", total)
	}
	// The settle delay is not optional: without it the probe hears the tail of
	// the sentence Helix just spoke and interrupts on its own voice.
	if bargeSettleDelay <= 0 {
		t.Error("the probe must let the speaker ring down before listening")
	}
}

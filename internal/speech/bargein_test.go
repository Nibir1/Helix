// internal/speech/bargein_test.go
// Purpose: BlackBox P12.5 — barge-in v2. A spoken reply must be interruptible
// mid-sentence, and the handle must be safe to call at any time.
package speech

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestStopSpeakingIsSafeWhenIdle(t *testing.T) {
	// Nothing in flight: a barge-in trigger must never panic or block. Wake
	// events and Ctrl+C can arrive at any moment, including when Helix is not
	// speaking at all.
	StopSpeaking()
	if Speaking() {
		t.Fatal("no reply in flight must report not speaking")
	}
}

func TestBeginSpeakingPublishesAndReleasesHandle(t *testing.T) {
	if Speaking() {
		t.Fatal("precondition: nothing should be speaking")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := beginSpeaking(cancel)
	if !Speaking() {
		t.Fatal("an in-flight reply must be visible to a barge-in trigger")
	}

	// The published handle must actually cancel the reply's context.
	StopSpeaking()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("StopSpeaking did not cancel the in-flight context")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", ctx.Err())
	}

	release()
	if Speaking() {
		t.Fatal("a released reply must no longer report speaking")
	}
}

func TestStopSpeakingIsRaceSafe(t *testing.T) {
	// A wake event, a keypress, and Ctrl+C can all fire at once. Run with
	// -race to make this meaningful.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := beginSpeaking(cancel)
	defer release()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			StopSpeaking()
			_ = Speaking()
		}()
	}
	wg.Wait()

	if ctx.Err() == nil {
		t.Fatal("concurrent barge-in triggers must still cancel the reply")
	}
}

// An already-cancelled context must short-circuit rather than speak: a reply
// the user already interrupted should not start playing.
func TestSpeakStreamRespectsPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := SpeakStream(ctx, "This reply should never be spoken. Not one word of it.")
	if err == nil {
		t.Fatal("a cancelled context must not produce speech")
	}
}

// Empty text is a no-op and must not publish a speaking handle.
func TestSpeakStreamEmptyTextIsNoOp(t *testing.T) {
	if err := SpeakStream(context.Background(), "   "); err != nil {
		t.Fatalf("empty text must be a silent no-op, got %v", err)
	}
	if Speaking() {
		t.Fatal("a no-op must not leave a speaking handle published")
	}
}

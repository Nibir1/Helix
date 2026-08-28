// internal/audio/ctxstream_test.go
// Purpose: BlackBox P12.5 — the context-aware streamer is the mechanism that
// makes barge-in cut a sentence mid-word instead of at its end.
package audio

import (
	"context"
	"testing"
)

// countingStreamer is an endless source that records how much was pulled.
type countingStreamer struct{ pulled int }

func (c *countingStreamer) Stream(out [][2]float64) (int, bool) {
	c.pulled += len(out)
	return len(out), true
}
func (c *countingStreamer) Err() error { return nil }

func TestCtxStreamerEndsStreamOnCancel(t *testing.T) {
	base := &countingStreamer{}
	ctx, cancel := context.WithCancel(context.Background())
	s := &ctxStreamer{base: base, ctx: ctx}

	buf := make([][2]float64, 64)

	// Before cancellation the wrapper is transparent.
	n, ok := s.Stream(buf)
	if !ok || n != len(buf) {
		t.Fatalf("expected a transparent pull, got n=%d ok=%v", n, ok)
	}

	cancel()

	// After cancellation it reports "finished", which is how a beep source
	// says stop — the speaker drains its current buffer and goes quiet.
	n, ok = s.Stream(buf)
	if ok {
		t.Fatal("a cancelled stream must report finished")
	}
	if n != 0 {
		t.Fatalf("a cancelled stream must yield no samples, got %d", n)
	}

	// And it must stop pulling from the source entirely.
	before := base.pulled
	_, _ = s.Stream(buf)
	if base.pulled != before {
		t.Fatal("a cancelled stream must not keep draining the source")
	}
}

func TestCtxStreamerPropagatesErr(t *testing.T) {
	base := &countingStreamer{}
	s := &ctxStreamer{base: base, ctx: context.Background()}
	if s.Err() != nil {
		t.Fatalf("Err must pass through the base streamer, got %v", s.Err())
	}
}

// A pre-cancelled context must be refused before any device work: a reply the
// user already interrupted should never reach the speaker.
func TestPlaySpeechContextRefusesCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := PlaySpeechContext(ctx, SpeechFormat{Kind: "pcm", SampleRate: 16000, Data: []byte{0, 0}}, 1)
	if err == nil {
		t.Fatal("a cancelled context must not play")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// PlaySpeech must remain exactly as it was for every existing caller.
func TestPlaySpeechStillRejectsUnknownFormat(t *testing.T) {
	err := PlaySpeech(SpeechFormat{Kind: "mp3", Data: []byte{1, 2, 3}}, 1)
	if err == nil {
		t.Fatal("mp3 is deliberately unsupported and must still be refused")
	}
}

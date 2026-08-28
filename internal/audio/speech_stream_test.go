// internal/audio/speech_stream_test.go
// Purpose: BlackBox P7.2c — the incremental PCM streamer never blocks the
// speaker callback, survives underruns, and reports a failed stream instead of
// playing silence forever.
package audio

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// pcmBytes builds n frames of 16-bit mono PCM at a constant amplitude.
func pcmBytes(frames int, v int16) []byte {
	b := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return b
}

func newTestStream(t *testing.T, r io.Reader) (*pcmStream, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return newPCMStream(ctx, StreamFormat{SampleRate: 24000, Channels: 1}, r), cancel
}

func TestPCMStreamDecodesFrames(t *testing.T) {
	s, _ := newTestStream(t, strings.NewReader(string(pcmBytes(100, 16000))))
	if err := s.preroll(); err != nil {
		t.Fatalf("preroll: %v", err)
	}

	out := make([][2]float64, 50)
	n, ok := s.Stream(out)
	if !ok || n == 0 {
		t.Fatalf("expected samples, got n=%d ok=%v", n, ok)
	}
	// 16000/32767 ≈ 0.488, and mono must be mirrored to both channels.
	if out[0][0] < 0.4 || out[0][0] > 0.6 {
		t.Fatalf("sample = %.3f, want ~0.49", out[0][0])
	}
	if out[0][0] != out[0][1] {
		t.Fatal("mono audio must be mirrored into both channels")
	}
}

// THE regression test. beep's Resampler discards the ok flag and treats ANY
// short read as a permanent end-of-stream (resample.go:102) — and a 24kHz TTS
// stream is ALWAYS resampled to the 44.1kHz device. Returning "here are 64
// samples, more coming" therefore truncated playback to roughly the preroll:
// the first sentence was cut after one word and the rest was dropped.
//
// So while the stream is live, Stream MUST fill the buffer completely.
func TestPCMStreamNeverShortReadsWhileLive(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	s, _ := newTestStream(t, pr)
	go func() { _, _ = pw.Write(pcmBytes(24000, 1000)) }()
	if err := s.preroll(); err != nil {
		t.Fatalf("preroll: %v", err)
	}

	// resamplerSingleBufferSize in beep is 512; use it as the realistic pull.
	out := make([][2]float64, 512)
	for i := 0; i < 300; i++ {
		n, ok := s.Stream(out)
		if !ok {
			t.Fatal("the stream ended while the producer was still live")
		}
		if n != len(out) {
			t.Fatalf("short read of %d/%d while live — beep's resampler would "+
				"treat this as end-of-stream and truncate playback", n, len(out))
		}
	}
}

// An underrun must not block the audio goroutine either.
func TestPCMStreamUnderrunDoesNotBlock(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	s, _ := newTestStream(t, pr)
	go func() { _, _ = pw.Write(pcmBytes(24000, 1000)) }()
	if err := s.preroll(); err != nil {
		t.Fatalf("preroll: %v", err)
	}

	out := make([][2]float64, 512)
	for i := 0; i < 200; i++ { // drain past what was written
		s.Stream(out)
	}

	done := make(chan bool, 1)
	go func() {
		_, ok := s.Stream(out)
		done <- ok
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("an underrun must not end the stream — the sentence would cut off")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stream blocked during an underrun; this would stall the audio device")
	}
}

func TestPCMStreamEndsAtEOF(t *testing.T) {
	s, _ := newTestStream(t, strings.NewReader(string(pcmBytes(10, 500))))
	if err := s.preroll(); err != nil {
		t.Fatalf("preroll: %v", err)
	}

	out := make([][2]float64, 256)
	for i := 0; i < 50; i++ {
		if _, ok := s.Stream(out); !ok {
			return // finished, as expected
		}
	}
	t.Fatal("a finished stream must eventually report done")
}

// Errors before any audio plays are the caller's cue to fall back to the
// buffered path, so preroll must surface them rather than hang.
func TestPrerollSurfacesEarlyFailure(t *testing.T) {
	pr, pw := io.Pipe()
	go func() { _ = pw.CloseWithError(errors.New("upstream 401")) }()

	s, _ := newTestStream(t, pr)
	err := s.preroll()
	if err == nil {
		t.Fatal("a stream that failed before producing audio must report an error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("the underlying cause must survive, got %v", err)
	}
}

func TestPrerollRejectsEmptyStream(t *testing.T) {
	s, _ := newTestStream(t, strings.NewReader(""))
	if err := s.preroll(); err == nil {
		t.Fatal("an empty synthesis is a failure, not a very short utterance")
	}
}

// Barge-in (P12.5) must stop a streamed reply just as it stops a buffered one.
func TestPCMStreamStopsOnCancel(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	go func() { _, _ = pw.Write(pcmBytes(48000, 1000)) }()

	s, cancel := newTestStream(t, pr)
	if err := s.preroll(); err != nil {
		t.Fatalf("preroll: %v", err)
	}

	cancel()

	out := make([][2]float64, 64)
	if _, ok := s.Stream(out); ok {
		t.Fatal("a cancelled stream must report finished so playback stops")
	}
}

func TestPlaySpeechStreamRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	if err := PlaySpeechStream(ctx, StreamFormat{SampleRate: 24000}, nil, StreamPlayback{}); err == nil {
		t.Error("a nil reader must be rejected")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	body := io.NopCloser(strings.NewReader(string(pcmBytes(10, 1))))
	if err := PlaySpeechStream(cancelled, StreamFormat{SampleRate: 24000}, body, StreamPlayback{}); err == nil {
		t.Error("an already-cancelled context must not play")
	}
}

// The preroll is the entire latency cost of streaming, so it must stay small.
func TestPrerollBudgetIsSmall(t *testing.T) {
	s := &pcmStream{format: StreamFormat{SampleRate: 24000, Channels: 1}, frameLen: 2}
	bytes := s.prerollBytes()
	ms := bytes * 1000 / (24000 * 2)
	if ms != prerollMillis {
		t.Fatalf("preroll = %dms, want %dms", ms, prerollMillis)
	}
	if ms > 300 {
		t.Fatalf("preroll of %dms defeats the point — buffered synthesis was ~2280ms", ms)
	}
}

// The whole point of the metric fix: OnFirstAudio must fire when playback is
// about to START, not when it ends — otherwise a longer reply reports a worse
// latency even though the first word was heard just as fast.
func TestOnFirstAudioFiresAtPrerollNotAtEnd(t *testing.T) {
	pr, pw := io.Pipe()

	s, _ := newTestStream(t, pr)

	fired := make(chan struct{})
	go func() {
		// Enough to satisfy the preroll, then hold the stream open.
		_, _ = pw.Write(pcmBytes(24000, 1000))
	}()

	go func() {
		if err := s.preroll(); err != nil {
			return
		}
		close(fired)
	}()

	select {
	case <-fired:
		// Preroll completed while the producer is still live and the stream is
		// nowhere near finished — exactly the instant the metric wants.
	case <-time.After(3 * time.Second):
		t.Fatal("preroll did not complete while audio was still arriving")
	}

	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done {
		t.Fatal("first-audio fired only after the stream ended — that measures " +
			"the whole utterance, not time-to-first-audio")
	}
	_ = pw.Close()
}

// The hook must be optional: nothing should require a caller to supply it.
func TestStreamPlaybackHookIsOptional(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := io.NopCloser(strings.NewReader(string(pcmBytes(10, 1))))
	// No OnFirstAudio set; must not panic.
	if err := PlaySpeechStream(ctx, StreamFormat{SampleRate: 24000}, body, StreamPlayback{}); err == nil {
		t.Error("a cancelled context must still be reported")
	}
}

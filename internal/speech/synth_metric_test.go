// internal/speech/synth_metric_test.go
// Purpose: ownership of the /voice-status time-to-first-audio metric. The
// number describes ONE reply — "how long until the user heard the first word" —
// so exactly one call per reply may write it. Playback itself needs an audio
// device (see stream_speak_test.go), so these tests exercise the synthesis
// entry points SpeakStream composes rather than driving the speaker.
package speech

import (
	"context"
	"testing"
	"time"
)

// useGlobalRegistry installs reg as the process-wide registry for one test and
// restores the previous one afterwards.
func useGlobalRegistry(t *testing.T, reg *Registry) {
	t.Helper()
	mu.Lock()
	prev := registry
	registry = reg
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registry = prev
		mu.Unlock()
	})
}

// metricTestRegistry builds a global registry whose only TTS provider takes
// `delay` to synthesize.
func metricTestRegistry(t *testing.T, delay time.Duration) {
	t.Helper()
	reg := newTestRegistry(t)
	reg.RegisterTTS(&fakeTTS{name: "fake", delay: delay})
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "fake"}})
	useGlobalRegistry(t, reg)
}

// TestPipelinedSentencesKeepStreamedMetric is the QA regression itself.
//
// SpeakStream streams sentence 1 and then pipelines sentences 2..N behind the
// audio that is already playing. When those pipelined calls went through
// Synthesize they overwrote the metric with their own full synthesis time and
// streamed=false, so a reply that streamed perfectly reported the LAST
// sentence's latency labeled "buffered":
//
//	Last TTS time-to-first-audio: 1185ms (budget 800ms) [buffered — ...]
func TestPipelinedSentencesKeepStreamedMetric(t *testing.T) {
	// 200ms per sentence: comfortably distinguishable from sentence 1's 150ms
	// streamed first-audio time, and unmistakable if it leaks into the metric.
	metricTestRegistry(t, 200*time.Millisecond)

	// Sentence 1 streamed. This is exactly what speakOnceStreamed's
	// OnFirstAudio hook records at the instant playback begins.
	lastSynthMs.Store(150)
	lastSpeechStreamed.Store(true)

	// Sentences 2..N, as SpeakStream's producer fetches them.
	for _, s := range []string{"Second sentence.", "Third sentence."} {
		if _, err := synthesizeChain(context.Background(), s); err != nil {
			t.Fatalf("synthesizeChain(%q): %v", s, err)
		}
	}

	if !LastSpeechStreamed() {
		t.Error("a streamed reply reported \"buffered\": a pipelined sentence " +
			"claimed the reply-level metric")
	}
	if got := LastSynthesizeLatencyMs(); got != 150 {
		t.Errorf("time-to-first-audio = %dms, want sentence 1's 150ms "+
			"(a pipelined sentence's synthesis time overwrote it)", got)
	}
}

// The reply-level entry point must still claim the metric — otherwise the
// buffered path would report nothing at all.
func TestSynthesizeClaimsMetric(t *testing.T) {
	metricTestRegistry(t, 120*time.Millisecond)

	// Pretend a previous reply streamed, so a stale true would be visible.
	lastSynthMs.Store(1)
	lastSpeechStreamed.Store(true)

	if _, err := Synthesize(context.Background(), "One buffered reply."); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	if LastSpeechStreamed() {
		t.Error("the buffered path must label itself buffered, not inherit " +
			"a previous reply's streamed flag")
	}
	if got := LastSynthesizeLatencyMs(); got < 100 {
		t.Errorf("latency = %dms, want ≳120ms (the measured round trip)", got)
	}
}

// A buffered FALLBACK must stay honest: when sentence 1 cannot stream, the
// status line has to say buffered even though SpeakStream was used.
func TestBufferedFallbackReportsBuffered(t *testing.T) {
	metricTestRegistry(t, 10*time.Millisecond)

	lastSpeechStreamed.Store(true) // stale streamed flag from an earlier reply

	// speakOnce's fallback leg: the streaming attempt failed (the fake provider
	// implements no streaming), so the reply is synthesized whole.
	if _, err := Synthesize(context.Background(), "Fallback reply."); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if LastSpeechStreamed() {
		t.Error("a reply that fell back to buffered synthesis must not be " +
			"reported as streamed")
	}
}

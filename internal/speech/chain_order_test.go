// internal/speech/chain_order_test.go
//
// Purpose: the provider the user chose is the provider that speaks.
//
// Reported from a live session: "I don't think it's using CSM at all, rather
// piper-local... no conversational pattern at all, stale walkie-talkie style."
// The logs agreed. csm-local.log ended at "Starting server" with not one
// synthesis request, while piper-local.log showed POST /synthesize repeating.
//
// The cause was a capability silently outranking a choice. SynthesizeStream
// walked the chain and `continue`d past any provider that could not stream, so
// with [csm-local, piper-local] it skipped csm, streamed with piper, and
// succeeded — and speakOnce therefore never reached the buffered path where
// the chain order is honoured. Everything about the setup was correct; the
// primary was simply never asked.
package speech

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"helix/internal/providers"
)

// bufferedTTS can synthesize but not stream — csm-local's shape.
type bufferedTTS struct {
	name  string
	calls int
	err   error
}

func (b *bufferedTTS) Name() string         { return b.name }
func (b *bufferedTTS) DisplayName() string  { return b.name }
func (b *bufferedTTS) SetAPIKey(string)     {}
func (b *bufferedTTS) RequiresAPIKey() bool { return false }
func (b *bufferedTTS) IsLocal() bool        { return true }
func (b *bufferedTTS) DefaultModel() string { return b.name }
func (b *bufferedTTS) Synthesize(context.Context, string, SynthesisOptions) (AudioFormat, error) {
	b.calls++
	if b.err != nil {
		return AudioFormat{}, b.err
	}
	return AudioFormat{Kind: KindWAV, SampleRate: 22050, Channels: 1, Bytes: []byte("audio")}, nil
}
func (b *bufferedTTS) HealthCheck(context.Context) error { return nil }

// streamingTTS can do both — piper-local's shape.
type streamingTTS struct {
	bufferedTTS
	streamCalls int
}

func (s *streamingTTS) SynthesizeStream(context.Context, string, SynthesisOptions) (StreamedAudio, error) {
	s.streamCalls++
	return StreamedAudio{}, nil
}

func chainRegistry(t *testing.T, primary, fallback TTSProvider, names ...string) *Registry {
	t.Helper()
	keys, err := providers.NewKeyStoreAt(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(keys, providers.NewHTTPClient(5e9))
	r.RegisterTTS(primary)
	r.RegisterTTS(fallback)
	r.SetConfig(Config{TTS: TTSConfig{Provider: names[0], Fallbacks: []string{names[1]}}})
	return r
}

// A buffered-only PRIMARY must not be skipped for a streaming fallback.
func TestBufferedPrimaryIsNotSkippedForAStreamingFallback(t *testing.T) {
	csm := &bufferedTTS{name: "csm-local"}
	piper := &streamingTTS{bufferedTTS: bufferedTTS{name: "piper-local"}}
	r := chainRegistry(t, csm, piper, "csm-local", "piper-local")

	// The streaming attempt must decline rather than hand the turn to piper.
	if _, used, err := r.SynthesizeStream(context.Background(), "hello", SynthesisOptions{}); err == nil {
		t.Fatalf("streaming returned %q for a chain whose primary cannot stream — "+
			"the fallback jumped the queue", used)
	}
	if piper.streamCalls != 0 {
		t.Errorf("the fallback was asked to stream %d time(s) while the primary "+
			"had not been tried at all", piper.streamCalls)
	}

	// The buffered path then honours the order.
	if _, err := r.Synthesize(context.Background(), "hello", SynthesisOptions{}); err != nil {
		t.Fatal(err)
	}
	if csm.calls != 1 {
		t.Errorf("the primary was called %d times, want 1 — it is chosen and "+
			"reachable, so it speaks", csm.calls)
	}
	if piper.calls != 0 {
		t.Errorf("the fallback spoke %d time(s) while the primary was working", piper.calls)
	}
}

// Streaming must still be used when the PRIMARY supports it — the fix must not
// cost the latency it was built for.
func TestStreamingPrimaryStillStreams(t *testing.T) {
	piper := &streamingTTS{bufferedTTS: bufferedTTS{name: "piper-local"}}
	csm := &bufferedTTS{name: "csm-local"}
	r := chainRegistry(t, piper, csm, "piper-local", "csm-local")

	if _, used, err := r.SynthesizeStream(context.Background(), "hello", SynthesisOptions{}); err != nil {
		t.Fatalf("a streaming primary did not stream: %v", err)
	} else if used != "piper-local" {
		t.Errorf("streamed with %q, want piper-local", used)
	}
}

// And a failing primary still falls back — the chain is an order, not a lock.
func TestFailingPrimaryStillFallsBack(t *testing.T) {
	csm := &bufferedTTS{name: "csm-local", err: errors.New("model not loaded")}
	piper := &streamingTTS{bufferedTTS: bufferedTTS{name: "piper-local"}}
	r := chainRegistry(t, csm, piper, "csm-local", "piper-local")

	if _, err := r.Synthesize(context.Background(), "hello", SynthesisOptions{}); err != nil {
		t.Fatalf("the chain did not fall back: %v", err)
	}
	if piper.calls != 1 {
		t.Errorf("the fallback spoke %d times, want 1", piper.calls)
	}
}

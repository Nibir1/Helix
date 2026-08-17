// internal/speech/tts_stream_test.go
// Purpose: BlackBox P7.2c — streamed synthesis requests a headerless format,
// walks the same failover chain, and degrades to the buffered path rather than
// ever becoming a new way to be silent.
package speech

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ttsStub(t *testing.T, handler http.HandlerFunc) *openaiTTS {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &openaiTTS{
		name: "openai", display: "OpenAI TTS",
		baseURL: srv.URL, model: "gpt-4o-mini-tts", voice: "alloy", key: "k",
	}
}

// The streaming path must request raw PCM, not WAV: a container would mean
// parsing a header out of bytes that may not have arrived yet.
func TestSynthesizeStreamRequestsHeaderlessPCM(t *testing.T) {
	var body map[string]any
	p := ttsStub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte("\x00\x01\x02\x03"))
	})

	got, err := p.SynthesizeStream(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer func() { _ = got.Body.Close() }()

	if body["response_format"] != "pcm" {
		t.Fatalf("response_format = %v, want \"pcm\"", body["response_format"])
	}
	// The rate is fixed by the API contract — that is what makes headerless
	// streaming safe.
	if got.SampleRate != openaiPCMSampleRate || got.Channels != 1 {
		t.Fatalf("format = %d Hz / %d ch, want %d/1", got.SampleRate, got.Channels, openaiPCMSampleRate)
	}
	if got.Body == nil {
		t.Fatal("the audio body must be returned unread")
	}
}

func TestSynthesizeStreamValidatesInput(t *testing.T) {
	p := ttsStub(t, func(w http.ResponseWriter, r *http.Request) {})

	if _, err := p.SynthesizeStream(context.Background(), "", SynthesisOptions{}); err == nil {
		t.Error("empty text must be rejected before a request is made")
	}

	keyless := &openaiTTS{name: "openai", baseURL: "http://127.0.0.1:1", model: "m", voice: "v"}
	if _, err := keyless.SynthesizeStream(context.Background(), "hi", SynthesisOptions{}); err == nil {
		t.Error("a missing key must be reported without calling out")
	}
}

// The buffered path stays untouched: it still asks for WAV, so an adapter that
// cannot stream is unaffected by this feature.
func TestBufferedPathStillRequestsWAV(t *testing.T) {
	var body map[string]any
	p := ttsStub(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write(makeWAV([]int16{1, 2, 3}, 24000, 1))
	})

	if _, err := p.Synthesize(context.Background(), "hello", SynthesisOptions{}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if body["response_format"] != "wav" {
		t.Fatalf("buffered path changed format to %v; it must stay wav", body["response_format"])
	}
}

// A chain with no streaming-capable provider is a normal condition that selects
// the buffered path — not an error the user should ever see.
func TestRegistryStreamReportsNoStreamingProvider(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterTTS(NewPiperTTS("http://127.0.0.1:1")) // buffered-only adapter
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "piper-local"}})

	_, _, err := reg.SynthesizeStream(context.Background(), "hi", SynthesisOptions{})
	if err == nil {
		t.Fatal("expected the no-streaming signal")
	}
	if err != errNoStreamingTTS {
		t.Fatalf("want errNoStreamingTTS so the caller falls back quietly, got %v", err)
	}
}

func TestRegistryStreamRequiresConfiguredChain(t *testing.T) {
	reg := newTestRegistry(t)
	if _, _, err := reg.SynthesizeStream(context.Background(), "hi", SynthesisOptions{}); err == nil {
		t.Fatal("an empty chain must be reported")
	} else if !strings.Contains(err.Error(), "voice-setup") {
		t.Fatalf("the error should point at the fix, got %v", err)
	}
}

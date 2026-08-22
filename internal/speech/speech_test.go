// internal/speech/speech_test.go
// Purpose: Registry failover semantics with fake providers — no network.
package speech

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"helix/internal/providers"
)

// fakeSTT is a scriptable STTProvider for chain testing.
type fakeSTT struct {
	name  string
	err   error
	text  string
	local bool
}

func (f *fakeSTT) Name() string                      { return f.name }
func (f *fakeSTT) DisplayName() string               { return f.name }
func (f *fakeSTT) SetAPIKey(string)                  {}
func (f *fakeSTT) HealthCheck(context.Context) error { return nil }
func (f *fakeSTT) RequiresAPIKey() bool              { return false }
func (f *fakeSTT) IsLocal() bool                     { return f.local }
func (f *fakeSTT) DefaultModel() string              { return "fake" }
func (f *fakeSTT) Transcribe(_ context.Context, _ AudioFormat) (Transcript, error) {
	if f.err != nil {
		return Transcript{}, f.err
	}
	return Transcript{Text: f.text, Provider: f.name}, nil
}

// fakeTTS is a scriptable TTSProvider for chain testing.
type fakeTTS struct {
	name  string
	err   error
	local bool
	// delay simulates synthesis time, so latency-metric tests can tell a
	// pipelined sentence's round trip apart from sentence 1's first-audio time.
	delay time.Duration
}

func (f *fakeTTS) Name() string                      { return f.name }
func (f *fakeTTS) DisplayName() string               { return f.name }
func (f *fakeTTS) SetAPIKey(string)                  {}
func (f *fakeTTS) HealthCheck(context.Context) error { return nil }
func (f *fakeTTS) RequiresAPIKey() bool              { return false }
func (f *fakeTTS) IsLocal() bool                     { return f.local }
func (f *fakeTTS) DefaultModel() string              { return "fake" }
func (f *fakeTTS) Synthesize(_ context.Context, _ string, _ SynthesisOptions) (AudioFormat, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return AudioFormat{}, f.err
	}
	return AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: []byte{1}}, nil
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	keys, err := providers.NewKeyStoreAt(filepath.Join(t.TempDir(), "secrets.json"))
	if err != nil {
		t.Fatalf("keystore: %v", err)
	}
	return NewRegistry(keys, providers.NewHTTPClient(5e9))
}

func TestRegistrySTTFailover(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "primary", err: errors.New("upstream 500")})
	reg.RegisterSTT(&fakeSTT{name: "backup", text: "failover works"})
	reg.SetConfig(Config{STT: STTConfig{Provider: "primary", Fallbacks: []string{"backup"}}})

	got, err := reg.Transcribe(context.Background(), AudioFormat{Kind: KindWAV, Bytes: []byte{0}})
	if err != nil {
		t.Fatalf("expected failover success, got: %v", err)
	}
	if got.Text != "failover works" || got.Provider != "backup" {
		t.Fatalf("wrong transcript: %+v", got)
	}
}

func TestRegistrySTTAllFail(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "a", err: errors.New("A down")})
	reg.RegisterSTT(&fakeSTT{name: "b", err: errors.New("B down")})
	reg.SetConfig(Config{STT: STTConfig{Provider: "a", Fallbacks: []string{"b"}}})

	_, err := reg.Transcribe(context.Background(), AudioFormat{Kind: KindWAV, Bytes: []byte{0}})
	if err == nil {
		t.Fatal("expected chain failure error")
	}
	for _, want := range []string{"a", "b", "A down", "B down"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestRegistryNoProviderConfigured(t *testing.T) {
	reg := newTestRegistry(t)
	if _, err := reg.Transcribe(context.Background(), AudioFormat{}); err == nil ||
		!strings.Contains(err.Error(), "/blackbox setup") {
		t.Fatalf("expected guidance error, got: %v", err)
	}
}

func TestRegistryTTSFailover(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterTTS(&fakeTTS{name: "primary", err: errors.New("quota exceeded")})
	reg.RegisterTTS(&fakeTTS{name: "backup"})
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "primary", Fallbacks: []string{"backup"}}})

	audio, err := reg.Synthesize(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("expected failover success, got: %v", err)
	}
	if audio.Kind != KindWAV {
		t.Fatalf("wrong audio: %+v", audio)
	}
}

func TestRegistryOfflineLocalFirst(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "cloud"})
	reg.RegisterSTT(&fakeSTT{name: "whisper-local", local: true})
	reg.RegisterTTS(&fakeTTS{name: "elevenlabs"})
	reg.RegisterTTS(&fakeTTS{name: "piper-local", local: true})
	reg.SetConfig(Config{
		STT: STTConfig{Provider: "cloud", Fallbacks: []string{"whisper-local"}},
		TTS: TTSConfig{Provider: "elevenlabs", Fallbacks: []string{"piper-local"}},
	})

	if got, want := reg.STTChain(), []string{"cloud", "whisper-local"}; !equalStrings(got, want) {
		t.Fatalf("online STT chain = %v, want %v", got, want)
	}

	reg.SetOffline(true)
	if got, want := reg.STTChain(), []string{"whisper-local", "cloud"}; !equalStrings(got, want) {
		t.Fatalf("offline STT chain = %v, want %v", got, want)
	}
	if got, want := reg.TTSChain(), []string{"piper-local", "elevenlabs"}; !equalStrings(got, want) {
		t.Fatalf("offline TTS chain = %v, want %v", got, want)
	}

	reg.SetOffline(false)
	if got, want := reg.STTChain(), []string{"cloud", "whisper-local"}; !equalStrings(got, want) {
		t.Fatalf("restored STT chain = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRegistryChainSkipsUnknown(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "real", text: "x"})
	reg.SetConfig(Config{STT: STTConfig{Provider: "ghost", Fallbacks: []string{"real", "real"}}})

	chain := reg.STTChain()
	if len(chain) != 1 || chain[0] != "real" {
		t.Fatalf("chain must resolve to registered, deduped names: %v", chain)
	}
}

func TestRegistryKeyHydration(t *testing.T) {
	reg := newTestRegistry(t)
	if err := reg.SetSTTKey("openai", "sk-test"); err == nil {
		t.Fatal("setting a key for an unregistered provider must fail")
	}

	// A mock that only transcribes requests carrying the hydrated key.
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"keyed"}`))
	}))
	defer srv.Close()

	reg.RegisterSTT(NewOpenAISTT("", srv.URL+"/v1"))
	reg.SetConfig(Config{STT: STTConfig{Provider: "openai"}})
	if err := reg.SetSTTKey("openai", "sk-live"); err != nil {
		t.Fatalf("set key: %v", err)
	}

	out, err := reg.Transcribe(context.Background(), AudioFormat{Kind: KindWAV, Bytes: makeWAV(nil, 16000, 1)})
	if err != nil {
		t.Fatalf("transcribe with hydrated key: %v", err)
	}
	if out.Text != "keyed" {
		t.Fatalf("wrong transcript: %+v", out)
	}
	if gotAuth != "Bearer sk-live" {
		t.Fatalf("hydrated key not applied; Authorization=%q", gotAuth)
	}
}

// TestRegistrySTTEmptyFallsThrough proves a provider that returns empty text
// (heard audio, no words) is treated as a retryable failure so the fallback
// chain gets a chance — and the voice loop can re-arm the mic instead of
// dispatching "" to the agent.
func TestRegistrySTTEmptyFallsThrough(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "primary", text: "  "}) // silence → empty
	reg.RegisterSTT(&fakeSTT{name: "backup", text: "got it"})
	reg.SetConfig(Config{STT: STTConfig{Provider: "primary", Fallbacks: []string{"backup"}}})

	got, err := reg.Transcribe(context.Background(), AudioFormat{Kind: KindWAV, Bytes: []byte{0}})
	if err != nil {
		t.Fatalf("expected fallback to succeed on empty primary, got: %v", err)
	}
	if got.Text != "got it" || got.Provider != "backup" {
		t.Fatalf("wrong transcript: %+v", got)
	}

	// All providers empty → ErrEmptyTranscript (retryable by the voice loop).
	reg2 := newTestRegistry(t)
	reg2.RegisterSTT(&fakeSTT{name: "only", text: ""})
	reg2.SetConfig(Config{STT: STTConfig{Provider: "only"}})
	_, err = reg2.Transcribe(context.Background(), AudioFormat{Kind: KindWAV, Bytes: []byte{0}})
	if err == nil || !errors.Is(err, ErrEmptyTranscript) {
		t.Fatalf("expected ErrEmptyTranscript, got: %v", err)
	}
}

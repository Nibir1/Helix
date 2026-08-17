// internal/speech/adapters_test.go
// Purpose: Adapter contract tests against in-process httptest mocks — zero
// real network, zero real keys (roadmap §9 rule 1).
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

func ctx(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func TestOpenAISTTMultipartAndResponse(t *testing.T) {
	var gotModel, gotAuth string
	var gotBytes int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		gotModel = r.FormValue("model")
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file", http.StatusBadRequest)
			return
		}
		gotBytes, _ = io.Copy(io.Discard, f)
		_ = f.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"hello blackbox","language":"en"}`))
	}))
	defer srv.Close()

	p := NewOpenAISTT("", srv.URL+"/v1")
	p.SetAPIKey("sk-x")
	out, err := p.Transcribe(ctx(t), AudioFormat{Kind: KindWAV, Bytes: makeWAV([]int16{1, 2, 3}, 16000, 1)})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if out.Text != "hello blackbox" || out.Language != "en" || out.Provider != "openai" {
		t.Fatalf("wrong transcript: %+v", out)
	}
	if gotModel != "whisper-1" {
		t.Errorf("model field = %q", gotModel)
	}
	if gotAuth != "Bearer sk-x" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBytes == 0 {
		t.Errorf("audio file part was empty")
	}
}

func TestGroqSTTContract(t *testing.T) {
	var gotModel, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		_ = r.ParseMultipartForm(10 << 20)
		gotModel = r.FormValue("model")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"groq heard you","language":"en"}`))
	}))
	defer srv.Close()

	p := NewGroqSTT("", srv.URL+"/openai/v1")
	p.SetAPIKey("gsk-x")
	if p.Name() != "groq" || !p.RequiresAPIKey() || p.IsLocal() {
		t.Fatalf("groq flags wrong: name=%s key=%v local=%v", p.Name(), p.RequiresAPIKey(), p.IsLocal())
	}
	out, err := p.Transcribe(ctx(t), AudioFormat{Kind: KindWAV, Bytes: makeWAV([]int16{1, 2, 3}, 16000, 1)})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if out.Text != "groq heard you" || out.Provider != "groq" {
		t.Fatalf("wrong transcript: %+v", out)
	}
	if gotModel != "whisper-large-v3-turbo" {
		t.Errorf("model field = %q", gotModel)
	}
	if gotAuth != "Bearer gsk-x" {
		t.Errorf("auth = %q", gotAuth)
	}
}

func TestDeepgramAuraTTSContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/speak" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Token dg-key" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.RawQuery, "encoding=linear16") {
			http.Error(w, "must request linear16", http.StatusBadRequest)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["text"] != "aura online" {
			t.Errorf("text = %q", body["text"])
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(makeWAV([]int16{5, -5}, 24000, 1))
	}))
	defer srv.Close()

	p := NewDeepgramTTS("", srv.URL+"/v1")
	p.SetAPIKey("dg-key")
	out, err := p.Synthesize(ctx(t), "aura online", SynthesisOptions{})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if out.Kind != KindWAV || out.SampleRate != 24000 {
		t.Fatalf("wrong audio format: %+v", out)
	}
}

func TestKokoroLocalSendsNoAuthRequired(t *testing.T) {
	p := NewKokoroLocalTTS("", "", "http://127.0.0.1:1/v1")
	if p.RequiresAPIKey() || !p.IsLocal() || p.Name() != "kokoro-local" {
		t.Fatalf("kokoro-local flags wrong: name=%s key=%v local=%v",
			p.Name(), p.RequiresAPIKey(), p.IsLocal())
	}
}

func TestOpenAISTTMissingKey(t *testing.T) {
	p := NewOpenAISTT("", "http://127.0.0.1:1")
	if _, err := p.Transcribe(ctx(t), AudioFormat{Kind: KindWAV, Bytes: makeWAV(nil, 16000, 1)}); err == nil ||
		!strings.Contains(err.Error(), "missing API key") {
		t.Fatalf("expected missing-key error, got: %v", err)
	}
}

func TestWhisperLocalSendsNoAuth(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"local"}`))
	}))
	defer srv.Close()

	p := NewWhisperLocalSTT("", srv.URL+"/v1")
	out, err := p.Transcribe(ctx(t), AudioFormat{Kind: KindWAV, Bytes: makeWAV([]int16{5}, 16000, 1)})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if out.Text != "local" {
		t.Fatalf("wrong transcript: %+v", out)
	}
	if gotAuth != "" {
		t.Errorf("local sidecar must not receive Authorization, got %q", gotAuth)
	}
	if p.RequiresAPIKey() || !p.IsLocal() {
		t.Errorf("whisper-local flags wrong: key=%v local=%v", p.RequiresAPIKey(), p.IsLocal())
	}
}

func TestOpenAITTSRequestAndWAVResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o-mini-tts" || body["response_format"] != "wav" {
			t.Errorf("unexpected payload: %+v", body)
		}
		if body["input"] != "systems nominal" {
			t.Errorf("input = %v", body["input"])
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(makeWAV([]int16{100, -100}, 22050, 1))
	}))
	defer srv.Close()

	p := NewOpenAITTS("", "", srv.URL+"/v1")
	p.SetAPIKey("sk-y")
	out, err := p.Synthesize(ctx(t), "systems nominal", SynthesisOptions{})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if out.Kind != KindWAV || out.SampleRate != 22050 || out.Channels != 1 {
		t.Fatalf("wrong audio format: %+v", out)
	}
}

func TestElevenLabsHeadersAndPCM(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/text-to-speech/") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("xi-api-key") != "el-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.RawQuery, "output_format=pcm_24000") {
			http.Error(w, "must request pcm", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte{0x01, 0x00, 0x02, 0x00})
	}))
	defer srv.Close()

	p := NewElevenLabsTTS("", "", srv.URL+"/v1")
	p.SetAPIKey("el-key")
	out, err := p.Synthesize(ctx(t), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if out.Kind != KindPCM || out.SampleRate != 24000 || out.Channels != 1 {
		t.Fatalf("wrong audio format: %+v", out)
	}
}

func TestDeepgramContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/listen" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Token dg-key" {
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.RawQuery, "model=nova-3") {
			http.Error(w, "missing model", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"metadata":{"language":"en"},"results":{"channels":[{"alternatives":[{"transcript":"deep works","confidence":0.97}]}]}}`))
	}))
	defer srv.Close()

	p := NewDeepgramSTT("", srv.URL+"/v1")
	p.SetAPIKey("dg-key")
	out, err := p.Transcribe(ctx(t), AudioFormat{Kind: KindWAV, Bytes: makeWAV([]int16{9}, 16000, 1)})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if out.Text != "deep works" || out.Confidence != 0.97 || out.Language != "en" {
		t.Fatalf("wrong transcript: %+v", out)
	}
}

func TestPiperSidecarContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tts" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("text") != "piper says hi" {
			http.Error(w, "missing text", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(makeWAV([]int16{7, 8}, 22050, 1))
	}))
	defer srv.Close()

	p := NewPiperTTS(srv.URL)
	out, err := p.Synthesize(ctx(t), "piper says hi", SynthesisOptions{})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if out.Kind != KindWAV || out.SampleRate != 22050 {
		t.Fatalf("wrong audio format: %+v", out)
	}
	if p.RequiresAPIKey() || !p.IsLocal() {
		t.Errorf("piper flags wrong: key=%v local=%v", p.RequiresAPIKey(), p.IsLocal())
	}
}

func TestDetectRecorderSmoke(t *testing.T) {
	// Machine-dependent: must never panic, and must return a known name or the
	// install-hint error.
	rec, err := DetectRecorder()
	if err == nil && rec != "sox" && rec != "ffmpeg" {
		t.Fatalf("unexpected recorder %q", rec)
	}
}

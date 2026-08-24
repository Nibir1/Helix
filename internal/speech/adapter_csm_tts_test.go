// internal/speech/adapter_csm_tts_test.go
// Purpose: pin the csm-local wire contract against a server shaped like the real
// csm.rs, for the same reason local_sidecar_test.go exists — both local adapters
// once shipped speaking routes their upstream servers do not serve, and every
// mock in the repo agreed with the mock.
//
// The cases here are the ones that would fail against a real server while a
// careless test passed: the request must carry speaker_id (CSM ignores `voice`),
// it must NOT send an Authorization header (a loopback sidecar has no account and
// some servers 401 on an empty bearer), and the response must be treated as WAV
// at whatever rate the header declares rather than an assumed one.
package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// csmServer mimics csm.rs: POST /v1/audio/speech returning WAV, and nothing else.
func csmServer(t *testing.T, capture *map[string]any, hdr *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/speech" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if hdr != nil {
			*hdr = r.Header.Clone()
		}
		if capture != nil {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json", http.StatusBadRequest)
				return
			}
			*capture = body
		}
		w.Header().Set("Content-Type", "audio/wav")
		// CSM synthesizes at 24kHz mono.
		_, _ = w.Write(EncodeWAVPCM16(make([]byte, 2400*2), 24000, 1))
	}))
}

func TestCSMSendsSpeakerIDAndNoAuthHeader(t *testing.T) {
	var body map[string]any
	var hdr http.Header
	srv := csmServer(t, &body, &hdr)
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	out, err := p.Synthesize(context.Background(), "hello there", SynthesisOptions{})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if out.Kind != KindWAV || out.SampleRate != 24000 {
		t.Fatalf("expected 24kHz WAV, got kind=%s rate=%d", out.Kind, out.SampleRate)
	}

	// speaker_id is how CSM is conditioned; `voice` is decoration for schema
	// compatibility. Losing speaker_id would silently change who speaks.
	if _, ok := body["speaker_id"]; !ok {
		t.Errorf("request must carry speaker_id, got %v", body)
	}
	if body["input"] != "hello there" {
		t.Errorf("input not sent verbatim: %v", body["input"])
	}
	if _, ok := body["temperature"]; !ok {
		t.Error("request should carry temperature — CSM's prosody knob")
	}

	// A loopback sidecar has no account. An empty bearer token makes some
	// servers reject the request outright.
	if got := hdr.Get("Authorization"); got != "" {
		t.Errorf("no Authorization header should be sent to a local sidecar, got %q", got)
	}
}

// The per-call voice option must win over the configured speaker, so a caller
// can switch speaker without rebuilding the provider.
func TestCSMPerCallVoiceOverridesSpeaker(t *testing.T) {
	var body map[string]any
	srv := csmServer(t, &body, nil)
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	if _, err := p.Synthesize(context.Background(), "hi", SynthesisOptions{Voice: "3"}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if got := body["speaker_id"]; got != float64(3) {
		t.Fatalf("speaker_id = %v, want 3", got)
	}
}

// A voice name that is not a number must fall back to speaker 0 rather than
// failing: someone who typed "alloy" out of habit should still be spoken to.
func TestCSMNonNumericVoiceFallsBackToSpeakerZero(t *testing.T) {
	for _, v := range []string{"alloy", "", "  ", "-1", "not-a-number"} {
		if got := csmSpeakerFromVoice(v); got != 0 {
			t.Errorf("csmSpeakerFromVoice(%q) = %d, want 0", v, got)
		}
	}
	if got := csmSpeakerFromVoice("2"); got != 2 {
		t.Errorf("numeric voice should be honored, got %d", got)
	}
}

// A non-WAV body must error rather than being played at a guessed rate — that
// is how audio comes out at the wrong pitch.
func TestCSMRejectsNonWAVResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("this is not audio"))
	}))
	defer srv.Close()

	p := NewCSMLocalTTS("", "", srv.URL)
	if _, err := p.Synthesize(context.Background(), "hi", SynthesisOptions{}); err == nil {
		t.Fatal("a non-WAV body must be rejected")
	} else if !strings.Contains(err.Error(), "not WAV") {
		t.Errorf("error should name the cause, got %v", err)
	}
}

// Provider identity: the wizard, the pricing table and the failover chain all
// key on these.
func TestCSMProviderIdentity(t *testing.T) {
	p := NewCSMLocalTTS("", "", "")
	if p.Name() != "csm-local" {
		t.Errorf("name = %q", p.Name())
	}
	if !p.IsLocal() {
		t.Error("csm-local must report as local")
	}
	if p.RequiresAPIKey() {
		t.Error("a local sidecar must not require an API key")
	}
	if p.DefaultModel() != "sesame/csm-1b" {
		t.Errorf("default model = %q", p.DefaultModel())
	}
}

// The default port must not collide with the other local sidecars. whisper.cpp
// and llama.cpp both default to 8080 and csm.rs does too, so a user running a
// local chain — precisely the user who wants CSM — would collide on first launch.
func TestCSMDefaultPortAvoidsKnownCollisions(t *testing.T) {
	for _, taken := range []string{":8080", ":5000", ":8880"} {
		if strings.Contains(CSMDefaultEndpoint, taken) {
			t.Errorf("CSM default endpoint %s collides with a known sidecar port %s",
				CSMDefaultEndpoint, taken)
		}
	}
	if !strings.HasPrefix(CSMDefaultEndpoint, "http://127.0.0.1:") {
		t.Errorf("a local sidecar must bind loopback, got %s", CSMDefaultEndpoint)
	}
}

// An unreachable sidecar must produce guidance naming the configured port, not a
// raw dial error — the lesson from the kokoro diagnosis.
func TestCSMUnreachableExplainsItself(t *testing.T) {
	p := NewCSMLocalTTS("", "", "http://127.0.0.1:1")
	_, err := p.Synthesize(context.Background(), "hi", SynthesisOptions{})
	if err == nil {
		t.Fatal("expected a failure against a dead port")
	}
	msg := err.Error()
	if !strings.Contains(msg, "csm-local") || !strings.Contains(msg, "127.0.0.1:1") {
		t.Errorf("diagnosis should name the provider and endpoint, got %q", msg)
	}
	if !strings.Contains(msg, "csm-server") {
		t.Errorf("diagnosis should say how to start it, got %q", msg)
	}
}

// It must be registered and reachable through the ordinary chain, or none of the
// above matters in production.
func TestCSMRegisteredInChain(t *testing.T) {
	var body map[string]any
	srv := csmServer(t, &body, nil)
	defer srv.Close()

	reg := newTestRegistry(t)
	reg.RegisterTTS(NewCSMLocalTTS("", "", srv.URL))
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "csm-local"}})

	out, err := reg.Synthesize(context.Background(), "chain test", SynthesisOptions{})
	if err != nil {
		t.Fatalf("chain synthesize: %v", err)
	}
	if len(out.Bytes) == 0 {
		t.Fatal("chain returned no audio")
	}
}

// contextServer accepts or rejects the Helix context extension, recording what
// it saw. rejectContext models today's csm.rs: a strict deserializer that 400s
// on an unknown field.
func contextServer(t *testing.T, rejectContext bool, seen *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if seen != nil {
			*seen = append(*seen, body)
		}
		if _, has := body["context"]; has && rejectContext {
			http.Error(w, `{"error":"unknown field `+"`context`"+`"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(EncodeWAVPCM16(make([]byte, 2400*2), 24000, 1))
	}))
}

func sampleContext() []ConversationTurn {
	return []ConversationTurn{
		{Speaker: SpeakerUser, Text: "did the build pass",
			Audio: AudioFormat{Kind: KindWAV, SampleRate: 24000, Channels: 1,
				Bytes: EncodeWAVPCM16(make([]byte, 480*2), 24000, 1)}},
		{Speaker: SpeakerAssistant, Text: "two tests failed"},
	}
}

// A context-capable server must receive the turns, oldest first, with audio.
func TestCSMSendsConversationContext(t *testing.T) {
	var seen []map[string]any
	srv := contextServer(t, false, &seen)
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	if _, err := p.Synthesize(context.Background(), "both in the parser",
		SynthesisOptions{Context: sampleContext()}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one request, got %d", len(seen))
	}

	raw, ok := seen[0]["context"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("context not sent as two segments: %v", seen[0]["context"])
	}
	first, _ := raw[0].(map[string]any)
	if first["text"] != "did the build pass" {
		t.Errorf("context must be oldest-first, got %v", first["text"])
	}
	if first["speaker"] != float64(SpeakerUser) {
		t.Errorf("speaker not carried: %v", first["speaker"])
	}
	if s, _ := first["audio_b64"].(string); s == "" {
		t.Error("prior turn audio must be sent — it is what conditions the prosody")
	}

	// A turn with text but no audio still belongs: CSM conditions on text too.
	second, _ := raw[1].(map[string]any)
	if second["text"] != "two tests failed" {
		t.Errorf("text-only turn missing: %v", second)
	}
	if s, _ := second["audio_b64"].(string); s != "" {
		t.Error("a turn with no audio must not invent any")
	}
}

// THE IMPORTANT ONE. A server that rejects the extension must still speak: the
// request is retried without context and the provider stops trying for the rest
// of the session. Context can improve the voice, never remove it.
func TestCSMFallsBackWhenContextRejected(t *testing.T) {
	var seen []map[string]any
	srv := contextServer(t, true, &seen)
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	out, err := p.Synthesize(context.Background(), "hello",
		SynthesisOptions{Context: sampleContext()})
	if err != nil {
		t.Fatalf("a sidecar without context support must still speak: %v", err)
	}
	if len(out.Bytes) == 0 {
		t.Fatal("no audio returned after fallback")
	}
	if len(seen) != 2 {
		t.Fatalf("expected a context attempt then a retry, got %d requests", len(seen))
	}
	if _, has := seen[0]["context"]; !has {
		t.Error("first attempt should have carried context")
	}
	if _, has := seen[1]["context"]; has {
		t.Error("the retry must drop context")
	}

	// And it must not keep paying for the discovery on every reply.
	if _, err := p.Synthesize(context.Background(), "again",
		SynthesisOptions{Context: sampleContext()}); err != nil {
		t.Fatalf("second synthesis: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("expected one request for the second call, got %d total", len(seen))
	}
	if _, has := seen[2]["context"]; has {
		t.Error("context must not be retried once the sidecar has refused it")
	}
}

// No context configured means no context field — the request stays byte-identical
// to what a stateless server has always received.
func TestCSMOmitsContextWhenEmpty(t *testing.T) {
	var seen []map[string]any
	srv := contextServer(t, false, &seen)
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	if _, err := p.Synthesize(context.Background(), "hi", SynthesisOptions{}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if _, has := seen[0]["context"]; has {
		t.Error("no context should be sent when none was supplied")
	}
}

// A 5xx says nothing about context support, so it must NOT disable the feature —
// otherwise one restart of a busy sidecar permanently downgrades the voice.
func TestCSMServerErrorDoesNotDisableContext(t *testing.T) {
	// Every attempt is recorded, because the assertion is about WHAT was sent,
	// not how many times. providers.HTTPClient retries 5xx on its own, so a
	// request count here would be measuring the client's retry policy rather
	// than this adapter's fallback — which is what my first version of this
	// test did, and it failed for that reason rather than a real defect.
	var withContext, withoutContext int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, has := body["context"]; has {
			withContext++
		} else {
			withoutContext++
		}
		http.Error(w, "overloaded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	if _, err := p.Synthesize(context.Background(), "hi",
		SynthesisOptions{Context: sampleContext()}); err == nil {
		t.Fatal("a 500 must surface as an error")
	}

	if withContext == 0 {
		t.Fatal("the attempt should have carried context")
	}
	// The fallback must not fire: a 5xx says the server is broken or busy, not
	// that it dislikes the field. Dropping context here would mean one restart
	// of a loaded sidecar permanently downgrades the voice.
	if withoutContext != 0 {
		t.Errorf("a 5xx must not trigger the drop-context retry, saw %d context-free requests",
			withoutContext)
	}
	if csm, ok := p.(*csmTTS); ok && csm.ContextRejected() {
		t.Error("a server error must not be read as 'context unsupported'")
	}
}

// THE CASE THAT ACTUALLY HAPPENS TODAY. csm.rs derives Deserialize without
// deny_unknown_fields, so serde ACCEPTS an unknown `context` field and silently
// drops it. The request succeeds, the audio is fine, and nothing was
// conditioned on — which is indistinguishable from success unless the server
// says so. Helix must record that as "ignored" rather than let status claim
// conversational prosody it is not getting.
func TestCSMDetectsSilentlyIgnoredContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No X-CSM-Context-Segments header: an unpatched server.
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(EncodeWAVPCM16(make([]byte, 2400*2), 24000, 1))
	}))
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	if _, err := p.Synthesize(context.Background(), "hi",
		SynthesisOptions{Context: sampleContext()}); err != nil {
		t.Fatalf("an unpatched server must still speak: %v", err)
	}

	csm := p.(*csmTTS)
	honored, ignored, rejected := csm.ContextStatus()
	if honored {
		t.Error("a server that said nothing must not be recorded as honoring context")
	}
	if !ignored {
		t.Error("a missing X-CSM-Context-Segments header means the context was dropped")
	}
	if rejected {
		t.Error("silently ignoring is not rejecting — the request succeeded")
	}
}

// A patched server reports what it used, and Helix records that it is real.
func TestCSMRecordsHonoredContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		segs, _ := body["context"].([]any)

		w.Header().Set("X-CSM-Context-Segments", fmt.Sprint(len(segs)))
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(EncodeWAVPCM16(make([]byte, 2400*2), 24000, 1))
	}))
	defer srv.Close()

	p := NewCSMLocalTTS("", "0", srv.URL)
	if _, err := p.Synthesize(context.Background(), "hi",
		SynthesisOptions{Context: sampleContext()}); err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	honored, ignored, _ := p.(*csmTTS).ContextStatus()
	if !honored {
		t.Error("a reported segment count means the context was genuinely applied")
	}
	if ignored {
		t.Error("must not be recorded as ignored when the server reported a count")
	}
}

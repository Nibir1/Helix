// internal/speech/adapter_openai_realtime_stt_test.go
// Purpose: the realtime adapter's contract, and — more importantly — that its
// INFERRED parts are genuinely overridable.
//
// This adapter guesses at a wire format that is not publicly documented, and a
// wrong guess fails silently. The tests that matter most here are not the happy
// path; they are the ones proving the session JSON is sent verbatim, that both
// framings work, and that a rejected session reports a fault instead of
// looking like a quiet room.
package speech

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// realtimeServer accepts one session, hands every client frame to inspect, and
// replies with the frames in reply.
func realtimeServer(t *testing.T, inspect func(typ websocket.MessageType, data []byte), reply ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		ctx := context.Background()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				typ, data, rerr := conn.Read(ctx)
				if rerr != nil {
					return
				}
				if inspect != nil {
					inspect(typ, data)
				}
				if typ == websocket.MessageText && strings.Contains(string(data), "commit") {
					return
				}
			}
		}()
		<-done
		for _, frame := range reply {
			_ = conn.Write(ctx, websocket.MessageText, []byte(frame))
		}
	}))
}

// feed sends one clip then closes, which is how streamingVoiceTurn signals the
// end of an utterance.
func feed(clips ...AudioFormat) <-chan AudioFormat {
	ch := make(chan AudioFormat, len(clips))
	for _, c := range clips {
		ch <- c
	}
	close(ch)
	return ch
}

func pcmClip() AudioFormat {
	return AudioFormat{Kind: KindPCM, SampleRate: 16000, Channels: 1,
		Bytes: make([]byte, 640)}
}

func collect(t *testing.T, stream <-chan Transcript) []Transcript {
	t.Helper()
	var got []Transcript
	deadline := time.After(5 * time.Second)
	for {
		select {
		case tr, ok := <-stream:
			if !ok {
				return got
			}
			got = append(got, tr)
		case <-deadline:
			t.Fatal("timed out waiting for transcripts")
		}
	}
}

// Only realtime models take the socket. whisper-1 must NOT, or every batch
// turn would attempt a dial and fall back — a wasted round trip per turn, and
// it would make Registry.StreamingSTT()'s type assertion a lie.
func TestOnlyRealtimeModelsAdvertiseStreaming(t *testing.T) {
	batch := NewOpenAISTT("whisper-1", "https://example.invalid/v1")
	if _, ok := batch.(StreamingSTTProvider); ok {
		t.Error("whisper-1 advertises streaming — it would dial a socket every turn")
	}
	live := NewOpenAIRealtimeSTT(openaiRealtimeSTTModel, "https://example.invalid/v1", nil)
	if _, ok := live.(StreamingSTTProvider); !ok {
		t.Error("gpt-live-transcribe does not advertise streaming")
	}

	if !IsRealtimeSTTModel("gpt-live-transcribe") || !IsRealtimeSTTModel("gpt-realtime-2.1") {
		t.Error("a documented realtime model was not recognised")
	}
	// gpt-live-1 is a DIFFERENT endpoint with a different contract. Routing it
	// here would fail confusingly instead of honestly.
	for _, m := range []string{"whisper-1", "gpt-live-1", "whisper-large-v3-turbo", ""} {
		if IsRealtimeSTTModel(m) {
			t.Errorf("%q was routed to the realtime transport", m)
		}
	}
}

// No socket may open without a credential.
func TestRealtimeMissingKeyNeverDials(t *testing.T) {
	var dialed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		dialed.Store(true)
	}))
	defer srv.Close()

	p := NewOpenAIRealtimeSTT("", srv.URL+"/v1", nil).(StreamingSTTProvider)
	if _, err := p.Stream(context.Background(), feed()); err == nil {
		t.Fatal("streamed without an API key")
	}
	if dialed.Load() {
		t.Error("a socket was opened without a credential")
	}
}

// The happy path, and the segment rule that the Deepgram incident taught:
// streamingVoiceTurn returns the turn on the FIRST IsFinal, so a per-segment
// final must be emitted as an interim or a sentence gets cut at a pause.
func TestRealtimeAccumulatesSegmentsIntoOneFinal(t *testing.T) {
	srv := realtimeServer(t, nil,
		`{"type":"conversation.item.input_audio_transcription.delta","delta":"delete the"}`,
		`{"type":"conversation.item.input_audio_transcription.completed","transcript":"delete the temp"}`,
		`{"type":"conversation.item.input_audio_transcription.completed","transcript":"directory"}`,
	)
	defer srv.Close()

	p := NewOpenAIRealtimeSTT("", srv.URL+"/v1", nil).(StreamingSTTProvider)
	p.SetAPIKey("test-key")
	stream, err := p.Stream(context.Background(), feed(pcmClip()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	got := collect(t, stream)
	if len(got) == 0 {
		t.Fatal("no transcripts")
	}
	finals := 0
	for _, tr := range got {
		if tr.IsFinal {
			finals++
		}
	}
	if finals != 1 {
		t.Errorf("got %d final transcripts, want exactly 1: %+v", finals, got)
	}
	last := got[len(got)-1]
	if !last.IsFinal {
		t.Errorf("the last transcript is not final: %+v", last)
	}
	if last.Text != "delete the temp directory" {
		t.Errorf("final text = %q, want both segments joined", last.Text)
	}
}

// THE test that makes the escape hatch real rather than aspirational: an
// overridden session is sent BYTE FOR BYTE, so a specification this file
// guessed wrong can be corrected from config with no rebuild.
func TestRealtimeSendsTheOverriddenSessionVerbatim(t *testing.T) {
	custom := `{"type":"whatever.the.real.spec.is","session":{"format":"opus"}}`
	var firstFrame atomic.Value
	srv := realtimeServer(t, func(_ websocket.MessageType, data []byte) {
		if _, seen := firstFrame.Load().(string); !seen {
			firstFrame.Store(string(data))
		}
	}, `{"type":"x.completed","transcript":"ok"}`)
	defer srv.Close()

	p := NewOpenAIRealtimeSTT("", srv.URL+"/v1", &RealtimeConfig{
		Session: json.RawMessage(custom),
	}).(StreamingSTTProvider)
	p.SetAPIKey("test-key")
	stream, err := p.Stream(context.Background(), feed(pcmClip()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	collect(t, stream)

	got, _ := firstFrame.Load().(string)
	if got != custom {
		t.Errorf("session frame = %q, want it verbatim (%q)", got, custom)
	}
}

// Both framings have to work, because which one is right is a guess.
func TestRealtimeAudioFramingOverride(t *testing.T) {
	clip := pcmClip()

	t.Run("json_base64", func(t *testing.T) {
		var sawAudio atomic.Bool
		srv := realtimeServer(t, func(typ websocket.MessageType, data []byte) {
			var f struct {
				Type  string `json:"type"`
				Audio string `json:"audio"`
			}
			if json.Unmarshal(data, &f) == nil && strings.Contains(f.Type, "append") {
				if raw, derr := base64.StdEncoding.DecodeString(f.Audio); derr == nil &&
					len(raw) == len(clip.Bytes) {
					sawAudio.Store(true)
				}
			}
		}, `{"type":"x.completed","transcript":"ok"}`)
		defer srv.Close()

		p := NewOpenAIRealtimeSTT("", srv.URL+"/v1", nil).(StreamingSTTProvider)
		p.SetAPIKey("k")
		stream, err := p.Stream(context.Background(), feed(clip))
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		collect(t, stream)
		if !sawAudio.Load() {
			t.Error("audio did not arrive as a base64 append event")
		}
	})

	t.Run("binary", func(t *testing.T) {
		var sawBinary atomic.Bool
		srv := realtimeServer(t, func(typ websocket.MessageType, data []byte) {
			if typ == websocket.MessageBinary && len(data) == len(clip.Bytes) {
				sawBinary.Store(true)
			}
		}, `{"type":"x.completed","transcript":"ok"}`)
		defer srv.Close()

		p := NewOpenAIRealtimeSTT("", srv.URL+"/v1",
			&RealtimeConfig{AudioFraming: "binary"}).(StreamingSTTProvider)
		p.SetAPIKey("k")
		stream, err := p.Stream(context.Background(), feed(clip))
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		collect(t, stream)
		if !sawBinary.Load() {
			t.Error("audio did not arrive as binary linear16 frames")
		}
	})
}

// A session that transcribes nothing AND reports an error must say so. Without
// this the turn reports ErrNoSpeech and the user is told their microphone is
// broken when the adapter's inferred wire format is what failed.
func TestRealtimeRejectedSessionReportsAFault(t *testing.T) {
	srv := realtimeServer(t, nil,
		`{"type":"error","error":{"message":"unknown parameter: input_audio_format"}}`)
	defer srv.Close()

	p := NewOpenAIRealtimeSTT("", srv.URL+"/v1", nil)
	p.SetAPIKey("k")
	sp := p.(StreamingSTTProvider)
	stream, err := sp.Stream(context.Background(), feed(pcmClip()))
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := collect(t, stream); len(got) != 0 {
		t.Errorf("a rejected session produced transcripts: %+v", got)
	}

	reporter, ok := p.(StreamFaultReporter)
	if !ok {
		t.Fatal("the adapter cannot report a fault, so a wrong wire format is " +
			"indistinguishable from a quiet room")
	}
	fault := reporter.StreamFault()
	if fault == nil {
		t.Fatal("a rejected session reported no fault")
	}
	if !strings.Contains(fault.Error(), "realtime") {
		t.Errorf("the fault does not name the subsystem: %v", fault)
	}
	// It has to point at the fix, since the fix is a config paste.
	if !strings.Contains(fault.Error(), "realtime") ||
		!strings.Contains(fault.Error(), "config") {
		t.Errorf("the fault does not say how to correct it: %v", fault)
	}
}

// The tolerant parser is what keeps a wrong event-name guess non-fatal.
func TestRealtimeFrameParserIsTolerant(t *testing.T) {
	for _, tc := range []struct {
		frame string
		text  string
		kind  rtFrameKind
	}{
		{`{"type":"a.b.delta","delta":"hi"}`, "hi", rtDelta},
		{`{"type":"a.b.partial","text":"hi"}`, "hi", rtDelta},
		{`{"type":"a.b.completed","transcript":"done"}`, "done", rtSegmentFinal},
		{`{"type":"a.b.done","text":"done"}`, "done", rtSegmentFinal},
		{`{"type":"input_audio_buffer.speech_stopped"}`, "", rtUtteranceEnd},
		{`{"type":"error","text":"bad"}`, "bad", rtError},
		{`{"type":"session.created"}`, "", rtOther},
		{`not json at all`, "", rtOther},
		{`{}`, "", rtOther},
	} {
		text, kind := parseRealtimeFrame([]byte(tc.frame))
		if kind != tc.kind {
			t.Errorf("%s: kind = %v, want %v", tc.frame, kind, tc.kind)
		}
		if text != tc.text {
			t.Errorf("%s: text = %q, want %q", tc.frame, text, tc.text)
		}
	}
}

// The wizard cannot offer a provider with no catalogue row, so the option
// would be invisible without it.
func TestEmbeddedCatalogHasTheRealtimeRow(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	for _, e := range catalog {
		if e.Provider == "openai" && e.Kind == "stt" && e.Model == openaiRealtimeSTTModel {
			if e.Unit != "minute" || e.PricePerUnit <= 0 {
				t.Errorf("row priced wrongly: %+v", e)
			}
			if e.Recommended {
				t.Error("an adapter with an inferred wire format must not be " +
					"recommended: there is no streaming-STT failover, so it would " +
					"put the whole voice path behind a guess")
			}
			return
		}
	}
	t.Fatalf("no catalogue row for %s — /blackbox setup cannot offer it",
		openaiRealtimeSTTModel)
}

// The session frame is now VERIFIED against OpenAI's published transcription
// shape, not inferred. It is pinned field by field because the first version
// of this adapter guessed it and was wrong in four places — every one of them
// a flat key where the real API nests under session.audio.input, and every one
// of them rejected silently, since the socket still opens.
func TestRealtimeSessionFrameMatchesThePublishedShape(t *testing.T) {
	p := NewOpenAIRealtimeSTT("", "https://example.invalid/v1", nil).(*openaiRealtimeSTT)
	raw, err := p.sessionFrame()
	if err != nil {
		t.Fatalf("session frame: %v", err)
	}

	var f struct {
		Type    string `json:"type"`
		Session struct {
			Type  string `json:"type"`
			Audio struct {
				Input struct {
					Format struct {
						Type string `json:"type"`
						Rate int    `json:"rate"`
					} `json:"format"`
					Transcription struct {
						Model string `json:"model"`
					} `json:"transcription"`
					TurnDetection *struct{} `json:"turn_detection"`
				} `json:"input"`
			} `json:"audio"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}

	if f.Type != "session.update" {
		t.Errorf("type = %q, want session.update (it was transcription_session.update)", f.Type)
	}
	if f.Session.Type != "transcription" {
		t.Errorf("session.type = %q, want transcription", f.Session.Type)
	}
	if f.Session.Audio.Input.Format.Type != "audio/pcm" {
		t.Errorf("format.type = %q, want audio/pcm (it was the string \"pcm16\")",
			f.Session.Audio.Input.Format.Type)
	}
	// The rate must be OUR capture rate, not the one copied from the guide's
	// example: telling a server 24000 while sending 16000 is a promise about
	// audio that is not what arrives.
	if f.Session.Audio.Input.Format.Rate != realtimeCaptureRate {
		t.Errorf("format.rate = %d, want %d — the rate streamingVoiceTurn captures at",
			f.Session.Audio.Input.Format.Rate, realtimeCaptureRate)
	}
	if f.Session.Audio.Input.Transcription.Model != openaiRealtimeSTTModel {
		t.Errorf("transcription.model = %q, want %q",
			f.Session.Audio.Input.Transcription.Model, openaiRealtimeSTTModel)
	}
	if f.Session.Audio.Input.TurnDetection != nil {
		t.Error("turn_detection must be null — streamingVoiceTurn owns endpointing, " +
			"and two endpointers disagreeing is how a turn got cut mid-sentence")
	}
}

// The beta header must NOT be sent: the realtime guide says to remove it when
// calling the GA interface, so sending it asks to be served by a version that
// is no longer current. An earlier revision set it on "harmless" reasoning.
func TestRealtimeDoesNotSendTheBetaHeader(t *testing.T) {
	p := NewOpenAIRealtimeSTT("", "https://example.invalid/v1", nil).(*openaiRealtimeSTT)
	p.SetAPIKey("k")
	h := p.realtimeHeaders("")
	if got := h.Get("OpenAI-Beta"); got != "" {
		t.Errorf("OpenAI-Beta = %q, want it absent on the GA interface", got)
	}
	if got := h.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization = %q", got)
	}

	// Still addable for anyone pointing at an older deployment.
	p2 := NewOpenAIRealtimeSTT("", "https://example.invalid/v1",
		&RealtimeConfig{Headers: map[string]string{"OpenAI-Beta": "realtime=v1"}}).(*openaiRealtimeSTT)
	p2.SetAPIKey("k")
	if got := p2.realtimeHeaders("").Get("OpenAI-Beta"); got != "realtime=v1" {
		t.Errorf("an explicitly configured header was dropped: %q", got)
	}
}

// The route is MEASURED, not read off the model page's "supported endpoint"
// column — which is what the first version of this adapter did, and it dialled
// a route that answers 403 to a WebSocket and 404 to a GET.
//
// Pinned as a test because the wrong value looked more authoritative than the
// right one: /v1/realtime/transcription_sessions is what the documentation
// names, and /v1/realtime is what actually accepts a socket.
func TestRealtimeDialsTheRouteThatAcceptsASocket(t *testing.T) {
	p := NewOpenAIRealtimeSTT("", "https://api.openai.com/v1", nil).(*openaiRealtimeSTT)
	url := p.streamURL()

	if strings.Contains(url, "transcription_sessions") {
		t.Errorf("dialling %q — that route answers 403 to a WebSocket upgrade and "+
			"404 to a GET. The session TYPE makes a session transcription-only, "+
			"not the path.", url)
	}
	if !strings.Contains(url, "/v1/realtime?") {
		t.Errorf("stream URL = %q, want the /v1/realtime path", url)
	}
}

// The endpoint's most useful message nests one level down. Reading only the top
// level reported "no detail given" for the one error a misconfigured session is
// most likely to get.
func TestRealtimeErrorFrameReadsTheNestedMessage(t *testing.T) {
	// Captured verbatim from an unauthenticated dial to wss://api.openai.com/v1/realtime.
	frame := `{"type":"error","event_id":"event_x","error":{"type":"invalid_request_error","code":null,"message":"Missing bearer or basic authentication in header","param":null}}`
	text, kind := parseRealtimeFrame([]byte(frame))
	if kind != rtError {
		t.Fatalf("kind = %v, want rtError", kind)
	}
	if text != "Missing bearer or basic authentication in header" {
		t.Errorf("text = %q, want the nested error.message", text)
	}
}

// The query is `intent=transcription`, MEASURED. Reasoning picked the wrong
// value twice — first `model=gpt-live-transcribe` (rejected: "is a
// transcription model and cannot be used as the realtime session model"), then
// no query at all (rejected: "You must provide a model parameter"). Only
// `intent` produces session.created.
func TestRealtimeUsesTheTranscriptionIntent(t *testing.T) {
	p := NewOpenAIRealtimeSTT("", "https://api.openai.com/v1", nil).(*openaiRealtimeSTT)
	url := p.streamURL()
	if !strings.Contains(url, "intent=transcription") {
		t.Errorf("stream URL = %q, want intent=transcription", url)
	}
	if strings.Contains(url, "model=") {
		t.Errorf("stream URL %q carries a model parameter — the server rejects a "+
			"transcription model there and says to pass it in the session instead", url)
	}
}

// 24000 is a server-enforced FLOOR, not a preference: 16000 comes back as
// "integer below minimum value. Expected a value >= 24000". The provider
// declares it so the recorder can be opened to match.
func TestRealtimeRequiresTwentyFourKilohertz(t *testing.T) {
	p := NewOpenAIRealtimeSTT("", "https://example.invalid/v1", nil).(*openaiRealtimeSTT)

	var reporter CaptureRateReporter = p
	if got := reporter.CaptureRateHz(); got < 24000 {
		t.Errorf("CaptureRateHz = %d, want at least 24000 — the server rejects less", got)
	}

	raw, err := p.sessionFrame()
	if err != nil {
		t.Fatalf("session frame: %v", err)
	}
	var f struct {
		Session struct {
			Audio struct {
				Input struct {
					Format struct {
						Rate int `json:"rate"`
					} `json:"format"`
				} `json:"input"`
			} `json:"audio"`
		} `json:"session"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f.Session.Audio.Input.Format.Rate != reporter.CaptureRateHz() {
		t.Errorf("session says rate %d but the provider captures at %d — the two must "+
			"agree or the server is told about audio it does not receive",
			f.Session.Audio.Input.Format.Rate, reporter.CaptureRateHz())
	}
}

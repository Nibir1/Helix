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

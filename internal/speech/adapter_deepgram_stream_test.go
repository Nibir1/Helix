// internal/speech/adapter_deepgram_stream_test.go
// Purpose: Streaming STT tests — a mock Deepgram WebSocket server verifies
// interim/final routing, the registry exposes streaming capability, and the
// linear16 WAV decode round-trips.
package speech

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeStreamSTT adds a streaming implementation on top of fakeSTT for
// registry capability tests.
type fakeStreamSTT struct {
	fakeSTT
}

func (f *fakeStreamSTT) Stream(ctx context.Context, chunks <-chan AudioFormat) (<-chan Transcript, error) {
	out := make(chan Transcript, 1)
	go func() {
		defer close(out)
		for range chunks {
		}
		out <- Transcript{Text: "streamed", Provider: f.name, IsFinal: true}
	}()
	return out, nil
}

func TestDeepgramStreamingInterimAndFinal(t *testing.T) {
	var authed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Token test-key" {
			authed.Store(true)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		ctx := context.Background()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText && strings.Contains(string(data), "CloseStream") {
				break
			}
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(
			`{"type":"Results","is_final":false,"channel":{"alternatives":[{"transcript":"hello","confidence":0.5}]}}`))
		_ = conn.Write(ctx, websocket.MessageText, []byte(
			`{"type":"Results","is_final":true,"speech_final":true,"channel":{"alternatives":[{"transcript":"hello world","confidence":0.95}]},"metadata":{"language":"en"}}`))
	}))
	defer srv.Close()

	p := NewDeepgramStreamingSTT("", srv.URL+"/v1")
	p.SetAPIKey("test-key")
	streamer, ok := p.(StreamingSTTProvider)
	if !ok {
		t.Fatal("deepgram streaming adapter must satisfy StreamingSTTProvider")
	}

	chunks := make(chan AudioFormat, 2)
	wav := makeWAV([]int16{1, 2, 3, 4}, 16000, 1)
	chunks <- AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: wav}
	chunks <- AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1, Bytes: wav}
	close(chunks)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := streamer.Stream(ctx, chunks)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var got []Transcript
	for tr := range stream {
		got = append(got, tr)
	}

	if !authed.Load() {
		t.Fatal("stream must send the Deepgram Authorization header")
	}
	if len(got) != 2 {
		t.Fatalf("want interim + final (2 transcripts), got %d: %+v", len(got), got)
	}
	if got[0].IsFinal || got[0].Text != "hello" {
		t.Fatalf("first should be interim 'hello': %+v", got[0])
	}
	if !got[1].IsFinal || got[1].Text != "hello world" || got[1].Language != "en" {
		t.Fatalf("second should be final 'hello world' (en): %+v", got[1])
	}
}

// TestDeepgramStreamingAccumulatesSegments is the mid-utterance-pause
// regression test: `is_final` alone finalizes a SEGMENT, not the utterance.
// A natural pause used to truncate the command at the first segment; the
// utterance must instead accumulate until `speech_final` fires.
func TestDeepgramStreamingAccumulatesSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

		ctx := context.Background()
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText && strings.Contains(string(data), "CloseStream") {
				break
			}
		}
		// Segment 1 locks in at a natural pause (is_final, NOT speech_final)…
		_ = conn.Write(ctx, websocket.MessageText, []byte(
			`{"type":"Results","is_final":true,"channel":{"alternatives":[{"transcript":"delete the","confidence":0.9}]}}`))
		// …then the utterance completes.
		_ = conn.Write(ctx, websocket.MessageText, []byte(
			`{"type":"Results","is_final":true,"speech_final":true,"channel":{"alternatives":[{"transcript":"temp directory","confidence":0.9}]}}`))
	}))
	defer srv.Close()

	p := NewDeepgramStreamingSTT("", srv.URL+"/v1")
	p.SetAPIKey("test-key")
	streamer := p.(StreamingSTTProvider)

	chunks := make(chan AudioFormat, 1)
	chunks <- AudioFormat{Kind: KindWAV, SampleRate: 16000, Channels: 1,
		Bytes: makeWAV([]int16{1, 2, 3, 4}, 16000, 1)}
	close(chunks)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := streamer.Stream(ctx, chunks)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	var final *Transcript
	for tr := range stream {
		if tr.IsFinal {
			trCopy := tr
			final = &trCopy
		}
	}
	if final == nil {
		t.Fatal("no final transcript emitted")
	}
	if final.Text != "delete the temp directory" {
		t.Fatalf("segments must accumulate across pauses; got final %q", final.Text)
	}
}

func TestRegistryStreamingSTTSelection(t *testing.T) {
	reg := newTestRegistry(t)
	reg.RegisterSTT(&fakeSTT{name: "batch"})
	reg.RegisterSTT(&fakeStreamSTT{fakeSTT: fakeSTT{name: "stream"}})

	reg.SetConfig(Config{STT: STTConfig{Provider: "stream"}})
	s, ok := reg.StreamingSTT()
	if !ok || s.Name() != "stream" {
		t.Fatalf("streaming-capable primary must be returned, got ok=%v name=%q", ok, s.Name())
	}

	// Batch-only primary must not report streaming support.
	reg.SetConfig(Config{STT: STTConfig{Provider: "batch"}})
	if _, ok := reg.StreamingSTT(); ok {
		t.Fatal("batch-only primary must not report streaming support")
	}
}

func TestDecodeWAVPCM16(t *testing.T) {
	in := []int16{100, -100, 3000, -1}
	wav := makeWAV(in, 16000, 1)
	got, err := DecodeWAVPCM16(wav)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("want %d samples, got %d", len(in), len(got))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Fatalf("sample %d: want %d, got %d", i, in[i], got[i])
		}
	}
}

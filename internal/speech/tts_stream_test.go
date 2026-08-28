// internal/speech/tts_stream_test.go
// Purpose: BlackBox P7.2c — streamed synthesis requests a headerless format,
// walks the same failover chain, and degrades to the buffered path rather than
// ever becoming a new way to be silent.
package speech

import (
	"context"
	"encoding/binary"
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
	// fakeTTS implements TTSProvider only — the buffered-only shape. (Not piper:
	// that adapter streams now, by consuming its sidecar's WAV header.)
	reg.RegisterTTS(&fakeTTS{name: "buffered-only"})
	reg.SetConfig(Config{TTS: TTSConfig{Provider: "buffered-only"}})

	_, _, err := reg.SynthesizeStream(context.Background(), "hi", SynthesisOptions{})
	if err == nil {
		t.Fatal("expected the no-streaming signal")
	}
	if err != errNoStreamingTTS {
		t.Fatalf("want errNoStreamingTTS so the caller falls back quietly, got %v", err)
	}
}

// The piper sidecar has no headerless response format, so streaming it means
// consuming the RIFF header off the front and handing back the reader
// positioned at the PCM — with the rate and channel count taken from the header
// rather than assumed.
func TestPiperSynthesizeStreamConsumesWAVHeader(t *testing.T) {
	samples := []int16{1, -2, 3, -4, 5, -6}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The text reaches the server either way; which transport carries it
		// depends on the piper version. Current servers take a JSON body on
		// POST /synthesize, older ones a ?text= query on GET /.
		if got := piperRequestText(t, r); got != "hello" {
			t.Errorf("text = %q, want %q", got, "hello")
		}
		_, _ = w.Write(makeWAV(samples, 22050, 1))
	}))
	defer srv.Close()

	p, ok := NewPiperTTS(srv.URL).(StreamingTTSProvider)
	if !ok {
		t.Fatal("piper must implement StreamingTTSProvider")
	}
	got, err := p.SynthesizeStream(context.Background(), "hello", SynthesisOptions{})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	defer func() { _ = got.Body.Close() }()

	if got.SampleRate != 22050 || got.Channels != 1 {
		t.Errorf("format = %d Hz / %d ch, want 22050/1 (read from the header)",
			got.SampleRate, got.Channels)
	}
	// The body must start at the first PCM sample, not at "RIFF": the consumer
	// decodes fixed 16-bit frames and would play the header itself as noise.
	pcm, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(pcm) != len(samples)*2 {
		t.Fatalf("PCM length = %d bytes, want %d (header not fully consumed?)",
			len(pcm), len(samples)*2)
	}
	if int16(uint16(pcm[0])|uint16(pcm[1])<<8) != samples[0] {
		t.Errorf("first frame = % x, want the first sample %d", pcm[:2], samples[0])
	}
}

// A body Helix cannot decode as 16-bit PCM must be reported BEFORE any audio
// plays, so the caller falls back to the buffered path instead of streaming
// noise to the speaker.
func TestPiperSynthesizeStreamRejectsUndecodableBodies(t *testing.T) {
	cases := map[string][]byte{
		"not a RIFF container": []byte("<html>404 not found</html>...........,,,,,"),
		"truncated header":     []byte("RIFF"),
		"32-bit float PCM":     floatWAVHeader(22050, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body)
			}))
			defer srv.Close()

			p := NewPiperTTS(srv.URL).(StreamingTTSProvider)
			got, err := p.SynthesizeStream(context.Background(), "hi", SynthesisOptions{})
			if err == nil {
				_ = got.Body.Close()
				t.Fatal("an undecodable body must fail before playback, not stream noise")
			}
		})
	}
}

// floatWAVHeader builds a RIFF/WAVE header declaring IEEE-float samples: valid
// WAV that the fixed 16-bit PCM consumer cannot play.
func floatWAVHeader(rate, channels int) []byte {
	out := make([]byte, 0, 44)
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, 36)
	out = append(out, "WAVE"...)
	out = append(out, "fmt "...)
	out = binary.LittleEndian.AppendUint32(out, 16)
	out = binary.LittleEndian.AppendUint16(out, 3) // IEEE float
	out = binary.LittleEndian.AppendUint16(out, uint16(channels))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate))
	out = binary.LittleEndian.AppendUint32(out, uint32(rate*channels*4))
	out = binary.LittleEndian.AppendUint16(out, uint16(channels*4))
	out = binary.LittleEndian.AppendUint16(out, 32) // bits per sample
	out = append(out, "data"...)
	out = binary.LittleEndian.AppendUint32(out, 0)
	return out
}

func TestRegistryStreamRequiresConfiguredChain(t *testing.T) {
	reg := newTestRegistry(t)
	if _, _, err := reg.SynthesizeStream(context.Background(), "hi", SynthesisOptions{}); err == nil {
		t.Fatal("an empty chain must be reported")
	} else if !strings.Contains(err.Error(), "blackbox setup") {
		t.Fatalf("the error should point at the fix, got %v", err)
	}
}

// piperRequestText extracts the utterance from a piper request, whichever
// transport it used.
func piperRequestText(t *testing.T, r *http.Request) string {
	t.Helper()
	if q := r.URL.Query().Get("text"); q != "" {
		return q
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return ""
	}
	return body.Text
}

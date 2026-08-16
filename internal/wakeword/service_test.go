// internal/wakeword/service_test.go
// Purpose: Wake-word tests via synthetic PCM/WAV fixtures and fixture
// scanners — no microphone, no sidecar binary (roadmap §9).
package wakeword

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"helix/internal/speech"
)

// pcmTone builds raw 16-bit mono PCM: a sine wave at the given amplitude
// (0..1 of full scale).
func pcmTone(samples int, amplitude, freq float64, rate int) []byte {
	out := make([]byte, samples*2)
	for i := 0; i < samples; i++ {
		v := math.Sin(2*math.Pi*freq*float64(i)/float64(rate)) * amplitude
		out[2*i] = byte(int16(v * 32767))
		out[2*i+1] = byte(int16(v*32767) >> 8)
	}
	return out
}

func wavFromPCM(pcm []byte, rate int) speech.AudioFormat {
	var buf []byte
	buf = append(buf, "RIFF"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(36+len(pcm)))
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = binary.LittleEndian.AppendUint32(buf, 16)
	buf = binary.LittleEndian.AppendUint16(buf, 1)
	buf = binary.LittleEndian.AppendUint16(buf, 1)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rate))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rate*2))
	buf = binary.LittleEndian.AppendUint16(buf, 2)
	buf = binary.LittleEndian.AppendUint16(buf, 16)
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(pcm)))
	return speech.AudioFormat{Kind: speech.KindWAV, SampleRate: rate, Channels: 1, Bytes: append(buf, pcm...)}
}

func TestRMSQuietVsLoud(t *testing.T) {
	const rate = 16000
	quiet := wavFromPCM(pcmTone(rate/10, 0.02, 440, rate), rate) // ~-34dB
	loud := wavFromPCM(pcmTone(rate/10, 0.5, 440, rate), rate)   // ~-6dB

	qr, err := RMS(quiet)
	if err != nil {
		t.Fatalf("rms quiet: %v", err)
	}
	lr, err := RMS(loud)
	if err != nil {
		t.Fatalf("rms loud: %v", err)
	}
	if qr > 0.05 {
		t.Errorf("quiet RMS = %.3f, want <= 0.05", qr)
	}
	if lr < 0.35 {
		t.Errorf("loud RMS = %.3f, want >= 0.35", lr)
	}
}

func TestRMSRejectsGarbage(t *testing.T) {
	if _, err := RMS(speech.AudioFormat{Kind: "mp3", Bytes: []byte{1}}); err == nil {
		t.Fatal("mp3 must be rejected")
	}
	if _, err := RMS(speech.AudioFormat{Kind: speech.KindWAV, Bytes: []byte("junk")}); err == nil {
		t.Fatal("garbage wav must be rejected")
	}
}

func TestEnergyDetectorPresets(t *testing.T) {
	const rate = 16000
	speechChunk := wavFromPCM(pcmTone(rate/4, 0.35, 220, rate), rate)

	balanced := NewEnergyDetector(PresetBalanced)
	if _, woke, err := balanced.Wake(speechChunk); err != nil || !woke {
		t.Fatalf("normal speech must wake balanced: woke=%v err=%v", woke, err)
	}

	quiet := wavFromPCM(pcmTone(rate/4, 0.05, 220, rate), rate)
	strict := NewEnergyDetector(PresetStrict)
	if _, woke, _ := strict.Wake(quiet); woke {
		t.Fatal("quiet chunk must not wake strict")
	}

	unknown := NewEnergyDetector(Preset("gibberish"))
	if unknown.Threshold != PresetThresholds[PresetBalanced] {
		t.Fatal("unknown preset must default to balanced")
	}
}

func TestSidecarDetectorContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/predict" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Content-Type") != "audio/wav" {
			http.Error(w, "wrong content type", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scores":{"hey_helix":0.91,"other":0.2}}`))
	}))
	defer srv.Close()

	d := NewSidecarDetector(srv.URL, "hey helix", PresetBalanced)
	score, woke, err := d.Wake(wavFromPCM(pcmTone(1600, 0.3, 440, 16000), 16000))
	if err != nil {
		t.Fatalf("sidecar wake: %v", err)
	}
	if score != 0.91 || !woke {
		t.Fatalf("score=%v woke=%v, want 0.91/true", score, woke)
	}

	if err := d.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestSidecarDetectorUnreachable(t *testing.T) {
	d := NewSidecarDetector("http://127.0.0.1:1", "hey helix", PresetStrict)
	_, _, err := d.Wake(wavFromPCM(pcmTone(100, 0.3, 440, 16000), 16000))
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected unreachable error, got %v", err)
	}
}

// fixtureScanner replays scripted chunks, then errors (loop ends).
type fixtureScanner struct {
	chunks []speech.AudioFormat
	i      int
	closed bool
}

func (f *fixtureScanner) NextChunk(context.Context) (speech.AudioFormat, error) {
	if f.i >= len(f.chunks) {
		return speech.AudioFormat{}, fmt.Errorf("exhausted")
	}
	c := f.chunks[f.i]
	f.i++
	return c, nil
}

func (f *fixtureScanner) Close() error { f.closed = true; return nil }

type stubDetector struct{ woke bool }

func (s stubDetector) Wake(speech.AudioFormat) (float64, bool, error) {
	return 0.8, s.woke, nil
}

func TestServiceDebounceAndStop(t *testing.T) {
	scanner := &fixtureScanner{chunks: make([]speech.AudioFormat, 5)}
	svc, err := NewService(scanner, stubDetector{woke: true}, Config{
		Cooldown: 500 * time.Millisecond, ChunkTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// First wake fires immediately; the next two chunks land inside the
	// cooldown window and must be suppressed; then exhaustion closes the
	// channel.
	first, ok := <-events
	if !ok || first.Score != 0.8 {
		t.Fatalf("first wake missing: %+v ok=%v", first, ok)
	}
	second, ok := <-events
	if ok {
		t.Fatalf("cooldown must suppress the burst, got second event %+v", second)
	}

	if err := svc.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if !scanner.closed {
		t.Fatal("scanner must be closed on Stop")
	}
	cancel()
}

func TestServiceCancellationIsClean(t *testing.T) {
	scanner := &fixtureScanner{chunks: make([]speech.AudioFormat, 100)}
	svc, _ := NewService(scanner, stubDetector{woke: false}, Config{})

	ctx, cancel := context.WithCancel(context.Background())
	events, err := svc.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	cancel()
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("cancelled service must close, not emit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop after cancellation")
	}
}

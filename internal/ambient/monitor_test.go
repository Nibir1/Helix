// internal/ambient/monitor_test.go
// Purpose: Phase 6 integration seam — ChunkMonitor decodes a WAV chunk and
// dispatches the configured response mode; TeeScanner passes chunks through
// while observing them.
package ambient

import (
	"context"
	"encoding/binary"
	"math/rand"
	"testing"

	"helix/internal/speech"
)

func noiseWAV(t *testing.T, frames int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(7))
	pcm := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		s := int16(rng.Intn(60000) - 30000)
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(s))
	}

	var out []byte
	put32 := func(v uint32) { var b [4]byte; binary.LittleEndian.PutUint32(b[:], v); out = append(out, b[:]...) }
	put16 := func(v uint16) { var b [2]byte; binary.LittleEndian.PutUint16(b[:], v); out = append(out, b[:]...) }

	out = append(out, "RIFF"...)
	put32(uint32(36 + len(pcm)))
	out = append(out, "WAVE"...)
	out = append(out, "fmt "...)
	put32(16)
	put16(1)     // PCM
	put16(1)     // mono
	put32(8000)  // rate
	put32(16000) // byte rate
	put16(2)     // block align
	put16(16)    // bits
	out = append(out, "data"...)
	put32(uint32(len(pcm)))
	out = append(out, pcm...)
	return out
}

func TestChunkMonitorDispatchesVocal(t *testing.T) {
	svc := NewServiceFromOptions(0.5, ResponseVocal, map[Category]bool{CategoryLoudNoise: true})
	mon := NewChunkMonitor(svc)
	var spoken []string
	mon.OnSpeak = func(s string) { spoken = append(spoken, s) }

	mon.Observe(speech.AudioFormat{Kind: speech.KindWAV, Bytes: noiseWAV(t, 1024)})

	if len(spoken) == 0 {
		t.Fatal("loud-noise chunk must produce a vocal response")
	}
}

func TestChunkMonitorLogMode(t *testing.T) {
	svc := NewServiceFromOptions(0.5, ResponseLog, map[Category]bool{CategoryLoudNoise: true})
	mon := NewChunkMonitor(svc)
	var logged []Event
	mon.OnLog = func(ev Event) { logged = append(logged, ev) }

	mon.Observe(speech.AudioFormat{Kind: speech.KindWAV, Bytes: noiseWAV(t, 1024)})

	if len(logged) == 0 || logged[0].Category != CategoryLoudNoise {
		t.Fatalf("loud-noise chunk must be logged, got %+v", logged)
	}
}

func TestChunkMonitorIgnoresNonWAV(t *testing.T) {
	svc := NewServiceFromOptions(0.5, ResponseVocal, map[Category]bool{CategoryLoudNoise: true})
	mon := NewChunkMonitor(svc)
	called := false
	mon.OnSpeak = func(string) { called = true }

	// Non-WAV kind must be silently skipped.
	mon.Observe(speech.AudioFormat{Kind: speech.KindPCM, Bytes: noiseWAV(t, 1024)})
	if called {
		t.Fatal("non-WAV chunks must not dispatch responses")
	}
}

func TestTeeScannerPassesThrough(t *testing.T) {
	src := &fakeChunkSource{clip: speech.AudioFormat{Kind: speech.KindWAV, Bytes: noiseWAV(t, 256)}}
	mon := NewChunkMonitor(NewServiceFromOptions(0.5, ResponseIgnore, nil))
	tee := Tee(src, mon)

	clip, err := tee.NextChunk(context.Background())
	if err != nil {
		t.Fatalf("next chunk: %v", err)
	}
	if clip.Kind != speech.KindWAV {
		t.Fatalf("tee must pass the chunk through unchanged, got %v", clip.Kind)
	}
}

type fakeChunkSource struct {
	clip speech.AudioFormat
}

func (f *fakeChunkSource) NextChunk(context.Context) (speech.AudioFormat, error) {
	return f.clip, nil
}

func (f *fakeChunkSource) Close() error { return nil }

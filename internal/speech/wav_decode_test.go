// internal/speech/wav_decode_test.go
// Purpose: DecodeWAVMono (Phase 6) — 16-bit PCM and 32-bit float WAV buffers
// decode to mono float64 samples; malformed/unsupported buffers error.
package speech

import (
	"encoding/binary"
	"math"
	"testing"
)

func appendLE16(b []byte, v uint16) []byte {
	var buf [2]byte
	binary.LittleEndian.PutUint16(buf[:], v)
	return append(b, buf[:]...)
}

func appendLE32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

func buildWAV(format, bits, channels uint16, rate uint32, pcm []byte) []byte {
	var out []byte
	out = append(out, "RIFF"...)
	out = appendLE32(out, uint32(36+len(pcm)))
	out = append(out, "WAVE"...)
	out = append(out, "fmt "...)
	out = appendLE32(out, 16)
	out = appendLE16(out, format)
	out = appendLE16(out, channels)
	out = appendLE32(out, rate)
	out = appendLE32(out, rate*uint32(2*channels))
	out = appendLE16(out, uint16(2*channels))
	out = appendLE16(out, bits)
	out = append(out, "data"...)
	out = appendLE32(out, uint32(len(pcm)))
	out = append(out, pcm...)
	return out
}

func pcm16Bytes(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
	}
	return out
}

func TestDecodeWAVMonoPCM16(t *testing.T) {
	samples := []int16{1000, -1000, 2000, -2000}
	wav := buildWAV(1, 16, 1, 8000, pcm16Bytes(samples))

	got, err := DecodeWAVMono(wav)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(got))
	}
	if math.Abs(got[0]-1000.0/32767) > 1e-6 {
		t.Fatalf("sample 0 = %v, want ~0.0305", got[0])
	}
	if math.Abs(got[1]+1000.0/32767) > 1e-6 {
		t.Fatalf("sample 1 = %v, want ~-0.0305", got[1])
	}
}

func TestDecodeWAVMonoAveragesStereo(t *testing.T) {
	// Stereo: left=1000, right=2000 → mono average 1500.
	var pcm []byte
	for i := 0; i < 3; i++ {
		pcm = appendLE16(pcm, 1000)
		pcm = appendLE16(pcm, 2000)
	}
	wav := buildWAV(1, 16, 2, 8000, pcm)

	got, err := DecodeWAVMono(wav)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(got))
	}
	if math.Abs(got[0]-1500.0/32767) > 1e-6 {
		t.Fatalf("stereo average = %v, want ~0.0458", got[0])
	}
}

func TestDecodeWAVMonoFloat32(t *testing.T) {
	var pcm []byte
	for _, f := range []float32{0.5, -0.5, 1.0} {
		pcm = appendLE32(pcm, math.Float32bits(f))
	}
	wav := buildWAV(3, 32, 1, 8000, pcm)

	got, err := DecodeWAVMono(wav)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 3 || math.Abs(got[0]-0.5) > 1e-6 || math.Abs(got[1]+0.5) > 1e-6 {
		t.Fatalf("float32 decode wrong: %v", got)
	}
}

func TestDecodeWAVMonoRejects(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":   {},
		"not-wav": []byte("hello world, this is definitely not a wave file"),
		"bad-enc": buildWAV(0xFF, 16, 1, 8000, pcm16Bytes([]int16{1})),
		"no-fmt":  []byte("RIFF\x04\x00\x00\x00WAVE"),
	} {
		if _, err := DecodeWAVMono(data); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

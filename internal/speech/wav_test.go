// internal/speech/wav_test.go
// Purpose: WAV fixture builder + header parser tests.
package speech

import (
	"encoding/binary"
	"testing"
)

// makeWAV builds a minimal 16-bit PCM RIFF/WAVE buffer for tests.
func makeWAV(samples []int16, rate, channels int) []byte {
	data := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(s))
	}

	var buf []byte
	buf = append(buf, "RIFF"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(36+len(data)))
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = binary.LittleEndian.AppendUint32(buf, 16)
	buf = binary.LittleEndian.AppendUint16(buf, 1) // PCM
	buf = binary.LittleEndian.AppendUint16(buf, uint16(channels))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rate))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rate*channels*2))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(channels*2))
	buf = binary.LittleEndian.AppendUint16(buf, 16)
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(data)))
	buf = append(buf, data...)
	return buf
}

func TestWAVHeaderInfo(t *testing.T) {
	wav := makeWAV([]int16{100, -100, 3000}, 16000, 1)
	rate, channels, err := wavHeaderInfo(wav)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rate != 16000 || channels != 1 {
		t.Fatalf("got rate=%d channels=%d", rate, channels)
	}
}

func TestWAVHeaderInfoToleratesStaleDataSize(t *testing.T) {
	wav := makeWAV([]int16{1, 2, 3, 4, 5}, 22050, 2)
	// Corrupt the data-chunk size to zero, as a killed recorder might.
	binary.LittleEndian.PutUint32(wav[40:44], 0)
	rate, channels, err := wavHeaderInfo(wav)
	if err != nil {
		t.Fatalf("parser must tolerate stale data size: %v", err)
	}
	if rate != 22050 || channels != 2 {
		t.Fatalf("got rate=%d channels=%d", rate, channels)
	}
}

func TestWAVHeaderInfoRejectsGarbage(t *testing.T) {
	if _, _, err := wavHeaderInfo([]byte("not a wav at all, sorry")); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

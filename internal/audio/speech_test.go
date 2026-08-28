// internal/audio/speech_test.go
//
// Purpose: Verify speech decode paths without a real sound device: WAV/PCM
// conversion, unsupported-kind rejection, and disabled-output behavior.
package audio

import (
	"encoding/binary"
	"errors"
	"testing"
)

// makeTestWAV builds a minimal 16-bit PCM mono RIFF buffer.
func makeTestWAV(samples []int16, rate int) []byte {
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
	buf = binary.LittleEndian.AppendUint16(buf, 1)
	buf = binary.LittleEndian.AppendUint16(buf, 1)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rate))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(rate*2))
	buf = binary.LittleEndian.AppendUint16(buf, 2)
	buf = binary.LittleEndian.AppendUint16(buf, 16)
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(data)))
	return append(buf, data...)
}

func TestDecodeSpeechWAVMono(t *testing.T) {
	wav := makeTestWAV([]int16{16384, -16384, 32767}, 16000)
	streamer, sr, err := decodeSpeech(SpeechFormat{Kind: "wav", Data: wav})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sr != 16000 {
		t.Fatalf("sample rate = %d", sr)
	}

	out := make([][2]float64, 3)
	n, ok := streamer.Stream(out)
	if !ok || n != 3 {
		t.Fatalf("streamed n=%d ok=%v", n, ok)
	}
	if out[0][0] <= 0.49 || out[0][0] >= 0.51 { // 16384/32767 ≈ 0.5
		t.Errorf("sample[0] = %f, want ~0.5", out[0][0])
	}
	if out[1][0] >= -0.49 { // -16384/32767 ≈ -0.5
		t.Errorf("sample[1] = %f, want ~-0.5", out[1][0])
	}
	if out[0][0] != out[0][1] {
		t.Errorf("mono must be duplicated to both channels")
	}
}

func TestDecodeSpeechPCMStereo(t *testing.T) {
	// Two stereo frames: L=1.0, R=-1.0, then L=-1.0, R=1.0.
	pcm := []byte{
		0xFF, 0x7F, 0x01, 0x80,
		0x01, 0x80, 0xFF, 0x7F,
	}
	streamer, sr, err := decodeSpeech(SpeechFormat{Kind: "pcm", SampleRate: 24000, Channels: 2, Data: pcm})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sr != 24000 {
		t.Fatalf("sample rate = %d", sr)
	}

	out := make([][2]float64, 2)
	n, _ := streamer.Stream(out)
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
	if out[0][0] <= 0 || out[0][1] >= 0 {
		t.Errorf("frame 0 channels wrong: %+v", out[0])
	}
	if out[1][0] >= 0 || out[1][1] <= 0 {
		t.Errorf("frame 1 channels wrong: %+v", out[1])
	}
}

func TestDecodeSpeechRejectsMP3(t *testing.T) {
	_, _, err := decodeSpeech(SpeechFormat{Kind: "mp3", Data: []byte{0xFF, 0xFB}})
	if err == nil || !errors.Is(err, err) {
		t.Fatalf("mp3 must be rejected with guidance, got: %v", err)
	}
}

func TestDecodeSpeechRejectsGarbageWAV(t *testing.T) {
	if _, _, err := decodeSpeech(SpeechFormat{Kind: "wav", Data: []byte("junkjunkjunk")}); err == nil {
		t.Fatal("garbage WAV must be rejected")
	}
}

func TestPlaySpeechDisabledFailsClosed(t *testing.T) {
	SetEnabled(false)
	defer SetEnabled(true)

	err := PlaySpeech(SpeechFormat{Kind: "wav", Data: makeTestWAV([]int16{1}, 16000)}, 1.0)
	if err == nil {
		t.Fatal("disabled audio must return an error, not silently succeed")
	}
}

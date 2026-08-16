// internal/audio/speech.go
//
// Purpose: Spoken-response playback (BlackBox Phase 1, ADR-007). Decodes
// provider audio (WAV or raw 16-bit PCM — never MP3, keeping the build
// dependency-free) into a beep streamer and plays it through the owned
// speaker. Platform-decoded here, untagged; actual output goes through the
// backend_* files like every other sound.
package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/gopxl/beep/v2"
)

// SpeechFormat describes one synthesized speech clip for playback.
type SpeechFormat struct {
	Kind       string // "wav" | "pcm"
	SampleRate int    // required for "pcm"
	Channels   int    // default 1
	Data       []byte
}

// maxSpeechSeconds bounds playback so a corrupt or hostile provider response
// cannot pin the shell indefinitely.
const maxSpeechSeconds = 120

// ErrSpeechUnsupported is returned by silent builds (Linux without audio_cgo).
var ErrSpeechUnsupported = errors.New("speech output requires an audio backend (Linux: install libasound2-dev and rebuild with -tags audio_cgo)")

// PlaySpeech decodes and plays one synthesized clip, blocking until finished.
// volume is 0..1; values outside the range default to 1.
//
// Args:
//   - f: the clip to play.
//   - volume: output gain.
//
// Returns: error if audio is disabled/unavailable or the clip is undecodable.
// Complexity: O(len(Data)) decode + O(duration) playback.
func PlaySpeech(f SpeechFormat, volume float64) error {
	if !IsEnabled() {
		return errors.New("audio output is disabled (use /audio on)")
	}
	if err := Init(); err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	if !IsReady() {
		return errors.New("audio engine not ready")
	}

	streamer, sr, err := decodeSpeech(f)
	if err != nil {
		return err
	}

	if volume <= 0 || volume > 1 {
		volume = 1
	}

	var s beep.Streamer = streamer
	if sr != beep.SampleRate(SampleRate) {
		s = beep.Resample(3, sr, beep.SampleRate(SampleRate), s)
	}
	if volume < 1 {
		base := s
		v := volume
		s = beep.StreamerFunc(func(samples [][2]float64) (int, bool) {
			n, ok := base.Stream(samples)
			for i := range samples[:n] {
				samples[i][0] *= v
				samples[i][1] *= v
			}
			return n, ok
		})
	}

	return backendPlaySpeech(beep.Take(SampleRate*maxSpeechSeconds, s))
}

// decodeSpeech converts a SpeechFormat into a streamer plus its native rate.
func decodeSpeech(f SpeechFormat) (beep.Streamer, beep.SampleRate, error) {
	switch f.Kind {
	case "wav":
		samples, rate, err := convertWAV(f.Data)
		if err != nil {
			return nil, 0, err
		}
		return &sliceStreamer{samples: samples}, beep.SampleRate(rate), nil
	case "pcm":
		if f.SampleRate <= 0 {
			return nil, 0, errors.New("pcm speech: missing sample rate")
		}
		ch := f.Channels
		if ch <= 0 {
			ch = 1
		}
		return &sliceStreamer{samples: samplesFromPCM16(f.Data, ch)}, beep.SampleRate(f.SampleRate), nil
	default:
		return nil, 0, fmt.Errorf("speech playback: %q audio not supported (configure the provider for wav/pcm output)", f.Kind)
	}
}

// convertWAV parses a RIFF/WAVE buffer (PCM int16 or IEEE float32) into
// stereo float64 samples plus the source sample rate. The data length is
// derived from the buffer, tolerating recorders killed mid-write.
func convertWAV(data []byte) ([][2]float64, int, error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errors.New("speech: not a RIFF/WAVE buffer")
	}

	var (
		formatTag, channels, bits int
		rate                      int
		pcm                       []byte
	)

	offset := 12
	for offset+8 <= len(data) {
		id := string(data[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8
		end := body + length
		if end > len(data) {
			end = len(data)
		}

		switch id {
		case "fmt ":
			if length < 16 {
				return nil, 0, errors.New("speech: malformed fmt chunk")
			}
			formatTag = int(binary.LittleEndian.Uint16(data[body : body+2]))
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			rate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
		case "data":
			pcm = data[body:end]
		}

		offset = body + length
		if length%2 == 1 {
			offset++
		}
	}

	if channels == 0 || rate == 0 || pcm == nil {
		return nil, 0, errors.New("speech: incomplete WAV header")
	}

	switch {
	case formatTag == 1 && bits == 16:
		return samplesFromPCM16(pcm, channels), rate, nil
	case formatTag == 3 && bits == 32:
		return samplesFromF32(pcm, channels), rate, nil
	default:
		return nil, 0, fmt.Errorf("speech: unsupported WAV encoding (tag %d, %d-bit)", formatTag, bits)
	}
}

// samplesFromPCM16 deinterleaves 16-bit LE PCM into stereo float64 samples.
func samplesFromPCM16(data []byte, channels int) [][2]float64 {
	frames := len(data) / (2 * channels)
	out := make([][2]float64, frames)
	for i := 0; i < frames; i++ {
		base := i * 2 * channels
		out[i][0] = float64(int16(binary.LittleEndian.Uint16(data[base:]))) / 32767
		if channels > 1 {
			out[i][1] = float64(int16(binary.LittleEndian.Uint16(data[base+2:]))) / 32767
		} else {
			out[i][1] = out[i][0]
		}
	}
	return out
}

// samplesFromF32 deinterleaves IEEE float32 PCM into stereo float64 samples.
func samplesFromF32(data []byte, channels int) [][2]float64 {
	frames := len(data) / (4 * channels)
	out := make([][2]float64, frames)
	for i := 0; i < frames; i++ {
		base := i * 4 * channels
		out[i][0] = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[base:])))
		if channels > 1 {
			out[i][1] = float64(math.Float32frombits(binary.LittleEndian.Uint32(data[base+4:])))
		} else {
			out[i][1] = out[i][0]
		}
	}
	return out
}

// sliceStreamer plays a fixed in-memory sample buffer.
type sliceStreamer struct {
	samples [][2]float64
	pos     int
}

func (s *sliceStreamer) Stream(out [][2]float64) (int, bool) {
	if s.pos >= len(s.samples) {
		return 0, false
	}
	n := copy(out, s.samples[s.pos:])
	s.pos += n
	return n, true
}

func (s *sliceStreamer) Err() error { return nil }

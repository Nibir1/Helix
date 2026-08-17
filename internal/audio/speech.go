// internal/audio/speech.go
//
// Purpose: Spoken-response playback (BlackBox Phase 1, ADR-007). Decodes
// provider audio (WAV or raw 16-bit PCM — never MP3, keeping the build
// dependency-free) into a beep streamer and plays it through the owned
// speaker. Platform-decoded here, untagged; actual output goes through the
// backend_* files like every other sound.
package audio

import (
	"context"
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
	return PlaySpeechContext(context.Background(), f, volume)
}

// PlaySpeechContext is PlaySpeech that stops when ctx is cancelled — the
// mechanism behind barge-in v2 (BlackBox P12.5).
//
// Cancellation works by ending the STREAM rather than by interrupting the
// speaker: a wrapper reports "no more samples" once ctx is done, so playback
// stops at the next buffer boundary. With the engine's 50 ms buffer that is
// ~50 ms to silence, mid-sentence, without racing the audio backend or
// touching the platform-specific files.
//
// Args:
//   - ctx: cancellation for in-flight playback.
//   - f: the clip to play.
//   - volume: output gain (0..1; outside the range defaults to 1).
//
// Returns: ctx.Err() when interrupted, otherwise a playback error or nil.
// Complexity: O(len(Data)) decode + O(duration) playback.
func PlaySpeechContext(ctx context.Context, f SpeechFormat, volume float64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !IsEnabled() {
		return errors.New("audio output is disabled (use /audio on)")
	}

	// Decode BEFORE touching the device. An undecodable clip (an MP3 from a
	// misconfigured provider, a truncated body) is a caller error, and opening
	// the speaker to discover that is both wasteful and — under the race
	// detector on macOS — enough to trip a data race inside oto's CoreAudio
	// driver that has nothing to do with the clip.
	streamer, sr, err := decodeSpeech(f)
	if err != nil {
		return err
	}

	if err := Init(); err != nil {
		return fmt.Errorf("audio device: %w", err)
	}
	if !IsReady() {
		return errors.New("audio engine not ready")
	}

	if volume <= 0 || volume > 1 {
		volume = 1
	}

	s := streamer
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

	if err := backendPlaySpeech(beep.Take(SampleRate*maxSpeechSeconds,
		&ctxStreamer{base: s, ctx: ctx})); err != nil {
		return err
	}
	// Report interruption to the caller so a barge-in is distinguishable from
	// a clip that simply finished.
	return ctx.Err()
}

// ctxStreamer ends a beep stream once its context is cancelled. beep pulls
// samples, so returning (0, false) is how a source says "finished" — which is
// exactly the semantics barge-in needs, and it stops within one buffer.
type ctxStreamer struct {
	base beep.Streamer
	ctx  context.Context
}

func (c *ctxStreamer) Stream(out [][2]float64) (int, bool) {
	if c.ctx.Err() != nil {
		return 0, false
	}
	return c.base.Stream(out)
}

func (c *ctxStreamer) Err() error { return c.base.Err() }

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

// BackendName returns a human-readable name for this build's audio backend
// (BlackBox P10.3). On Linux it distinguishes the default CGO-free binary
// (silent) from an `-tags audio_cgo` build (speaker output).
func BackendName() string { return backendName() }

// SpeechSupported reports whether this build can play TTS audio at all.
// False means the binary is structurally silent — no provider, key, or config
// change will produce sound; the fix is a rebuild (docs/edge_deployment.md §3.1).
func SpeechSupported() bool { return backendSpeechSupported() }

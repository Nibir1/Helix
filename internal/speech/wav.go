// internal/speech/wav.go
// Purpose: Minimal RIFF/WAVE header parsing shared by adapters and tests.
// Tolerant by design: sidecar-recorded clips killed mid-write can carry a
// stale data-chunk size, so the data length is derived from the buffer, not
// the header.
package speech

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// wavHeaderInfo extracts sample rate and channel count from a RIFF/WAVE
// buffer. Supports PCM (format 1) and IEEE float (format 3) — enough for every
// provider and recorder Helix speaks to.
func wavHeaderInfo(data []byte) (sampleRate int, channels int, err error) {
	if len(data) < 44 {
		return 0, 0, errors.New("wav: buffer too short")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, 0, errors.New("wav: not a RIFF/WAVE buffer")
	}

	offset := 12
	var haveFmt bool
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkLen := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		body := offset + 8

		switch chunkID {
		case "fmt ":
			if chunkLen < 16 || body+16 > len(data) {
				return 0, 0, fmt.Errorf("wav: malformed fmt chunk")
			}
			format := binary.LittleEndian.Uint16(data[body : body+2])
			if format != 1 && format != 3 {
				return 0, 0, fmt.Errorf("wav: unsupported format tag %d", format)
			}
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			sampleRate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			haveFmt = true
		case "data":
			// Present is enough; length comes from the buffer itself.
		}

		if haveFmt && chunkID == "data" {
			return sampleRate, channels, nil
		}

		offset = body + chunkLen
		if chunkLen%2 == 1 {
			offset++ // chunks are word-aligned
		}
	}

	if haveFmt {
		return sampleRate, channels, nil
	}
	return 0, 0, errors.New("wav: no fmt chunk found")
}

// DecodeWAVMono decodes a RIFF/WAVE buffer (16-bit PCM or 32-bit float) into
// mono float64 samples in [-1,1]. Channels are averaged and the data length is
// derived from the buffer (tolerating recorders killed mid-write). Used by the
// ambient monitor (Phase 6).
func DecodeWAVMono(data []byte) ([]float64, error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF/WAVE buffer")
	}

	var (
		formatTag, channels, bits int
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
				return nil, errors.New("wav: malformed fmt chunk")
			}
			formatTag = int(binary.LittleEndian.Uint16(data[body : body+2]))
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
		case "data":
			pcm = data[body:end]
		}

		offset = body + length
		if length%2 == 1 {
			offset++
		}
	}

	if channels == 0 || pcm == nil {
		return nil, errors.New("wav: incomplete header")
	}

	switch {
	case formatTag == 1 && bits == 16:
		return pcm16Mono(pcm, channels), nil
	case formatTag == 3 && bits == 32:
		return f32Mono(pcm, channels), nil
	default:
		return nil, fmt.Errorf("wav: unsupported encoding (tag %d, %d-bit)", formatTag, bits)
	}
}

// DecodeWAVPCM16 decodes a RIFF/WAVE buffer into mono int16 samples
// (16-bit PCM or 32-bit float, channels averaged). Used by streaming STT,
// which sends raw linear16 PCM to the provider.
func DecodeWAVPCM16(data []byte) ([]int16, error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("wav: not a RIFF/WAVE buffer")
	}

	var (
		formatTag, channels, bits int
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
				return nil, errors.New("wav: malformed fmt chunk")
			}
			formatTag = int(binary.LittleEndian.Uint16(data[body : body+2]))
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
		case "data":
			pcm = data[body:end]
		}

		offset = body + length
		if length%2 == 1 {
			offset++
		}
	}

	if channels == 0 || pcm == nil {
		return nil, errors.New("wav: incomplete header")
	}

	switch {
	case formatTag == 1 && bits == 16:
		return pcm16MonoInt16(pcm, channels), nil
	case formatTag == 3 && bits == 32:
		return f32MonoInt16(pcm, channels), nil
	default:
		return nil, fmt.Errorf("wav: unsupported encoding (tag %d, %d-bit)", formatTag, bits)
	}
}

func pcm16MonoInt16(data []byte, channels int) []int16 {
	frames := len(data) / (2 * channels)
	out := make([]int16, frames)
	for i := 0; i < frames; i++ {
		var sum int32
		for c := 0; c < channels; c++ {
			off := i*2*channels + c*2
			sum += int32(int16(binary.LittleEndian.Uint16(data[off:])))
		}
		out[i] = int16(sum / int32(channels))
	}
	return out
}

func f32MonoInt16(data []byte, channels int) []int16 {
	frames := len(data) / (4 * channels)
	out := make([]int16, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < channels; c++ {
			off := i*4*channels + c*4
			sum += float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off:])))
		}
		out[i] = int16(math.Round((sum / float64(channels)) * 32767))
	}
	return out
}

func pcm16Mono(data []byte, channels int) []float64 {
	frames := len(data) / (2 * channels)
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < channels; c++ {
			off := i*2*channels + c*2
			sum += float64(int16(binary.LittleEndian.Uint16(data[off:])))
		}
		out[i] = sum / float64(channels) / 32767
	}
	return out
}

func f32Mono(data []byte, channels int) []float64 {
	frames := len(data) / (4 * channels)
	out := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for c := 0; c < channels; c++ {
			off := i*4*channels + c*4
			sum += float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off:])))
		}
		out[i] = sum / float64(channels)
	}
	return out
}

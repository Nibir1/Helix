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

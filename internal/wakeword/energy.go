// internal/wakeword/energy.go
// Purpose: Sidecar-free wake detection v1 — normalized RMS energy over a
// captured chunk with a per-preset threshold. This is deliberately honest:
// it detects "someone started speaking / a loud sound", not the phrase
// itself. Real keyword spotting uses the sidecar client (sidecar.go,
// ADR-002); energy mode is the everywhere-works fallback and the default.
package wakeword

import (
	"encoding/binary"
	"fmt"
	"math"

	"helix/internal/speech"
)

// PresetThresholds maps sensitivity presets to RMS wake thresholds
// (0..1 of full scale). Typical speech sits around 0.05–0.20.
var PresetThresholds = map[Preset]float64{
	PresetStrict:   0.18,
	PresetBalanced: 0.12,
	PresetLoose:    0.07,
}

// EnergyDetector wakes when a chunk's normalized RMS exceeds the preset
// threshold. It has no state beyond its threshold.
type EnergyDetector struct {
	Threshold float64
}

// NewEnergyDetector builds a detector from a sensitivity preset
// (defaults to balanced for unknown presets).
func NewEnergyDetector(preset Preset) *EnergyDetector {
	t, ok := PresetThresholds[preset]
	if !ok {
		t = PresetThresholds[PresetBalanced]
	}
	return &EnergyDetector{Threshold: t}
}

// RMS returns the normalized root-mean-square amplitude (0..1) of a clip.
// WAV and raw 16-bit PCM are accepted; other kinds error.
func RMS(clip speech.AudioFormat) (float64, error) {
	var pcm []byte
	switch clip.Kind {
	case speech.KindWAV:
		info, err := parseWAV(clip.Bytes)
		if err != nil {
			return 0, err
		}
		if info.BitsPerSample != 16 {
			return 0, fmt.Errorf("energy: %d-bit WAV unsupported", info.BitsPerSample)
		}
		pcm = info.Data
	case speech.KindPCM:
		pcm = clip.Bytes
	default:
		return 0, fmt.Errorf("energy: kind %q unsupported", clip.Kind)
	}

	n := len(pcm) / 2
	if n == 0 {
		return 0, nil
	}
	var sum float64
	for i := 0; i < n; i++ {
		v := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768
		sum += v * v
	}
	return math.Sqrt(sum / float64(n)), nil
}

// Wake scores one chunk: 0..1 = normalized RMS; woke=true when above the
// threshold.
func (d *EnergyDetector) Wake(clip speech.AudioFormat) (score float64, woke bool, err error) {
	score, err = RMS(clip)
	if err != nil {
		return 0, false, err
	}
	return score, score >= d.Threshold, nil
}

// wavInfo is the minimal parse result needed here (audio has its own richer
// decoder; this one is dependency-free and sidecar-safe).
type wavInfo struct {
	BitsPerSample int
	Data          []byte
}

func parseWAV(data []byte) (wavInfo, error) {
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return wavInfo{}, fmt.Errorf("energy: not a RIFF/WAVE buffer")
	}
	offset := 12
	var out wavInfo
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
			if length >= 16 {
				out.BitsPerSample = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			}
		case "data":
			out.Data = data[body:end]
		}
		offset = body + length
		if length%2 == 1 {
			offset++
		}
	}
	if out.Data == nil {
		return wavInfo{}, fmt.Errorf("energy: no data chunk")
	}
	return out, nil
}

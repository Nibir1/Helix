// internal/wakeword/energy.go
// Purpose: Sidecar-free wake detection v1 — chunk RMS measured against the
// room's OWN noise floor. This is deliberately honest: it detects "someone
// started speaking / a loud sound", not the phrase itself. Real keyword
// spotting uses the sidecar client (sidecar.go, ADR-002); energy mode is the
// everywhere-works fallback and the default.
//
// WHY THE FLOOR IS MEASURED AND NOT A CONSTANT. This detector shipped with
// absolute thresholds (balanced = 0.12 normalized RMS) fitted to synthetic
// full-scale sine fixtures, and it could not fire on a real microphone. A
// MacBook Pro built-in mic at input volume 38 measures ~0.0011 RMS for a quiet
// room and ~0.005 RMS for speech-level sound at conversational distance — so
// 0.12 was roughly 25x above anything the device produces, and hands-free wake
// said "listening" forever without ever hearing anything. speech/energy.go had
// the right order of magnitude all along ("quiet-but-real speech is typically
// >= 0.01 RMS"), 12x below the number this file was comparing against.
//
// An absolute number cannot be right anyway: the same voice lands at 0.005 on
// a laptop mic at low input gain and 0.15 on a close USB mic at unity, so any
// constant is wrong for most machines. What IS stable across mics, rooms and
// gain settings is the RATIO between speech and the room it is spoken in —
// speech sits ~10-25 dB over a quiet room's floor. So the floor is measured
// continuously and the presets are multiples of it, with one absolute
// audibility gate underneath so a muted or permission-denied microphone
// (whose dither is a thousandth of anything real) cannot wake on noise.
package wakeword

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	"helix/internal/speech"
)

// PresetRatios maps sensitivity presets to how far above the measured room
// noise floor a chunk must sit to count as someone speaking, as a linear
// multiple.
//
// Fitted to measurements on the machine that reported the bug, not to a guess.
// A MacBook Pro built-in mic at macOS input volume 38 yields RMS 0.012-0.033
// for a clearly audible voice, over a floor of 0.0011 in a quiet room and
// 0.0035 with the fan spinning. That is 3.4x-30x depending on the room, and
// the low end of that range — a quiet speaker in a noisy room — is what sets
// the ceiling on how demanding balanced may be. 2.5x leaves the shipped
// default reachable in both rooms while staying clear of floorRiseGate, which
// is the level a room's own fluctuation is allowed to reach (1.5x).
var PresetRatios = map[Preset]float64{
	PresetStrict:   4.0, // ~12 dB over the floor
	PresetBalanced: 2.5, // ~8 dB
	PresetLoose:    2.0, // ~6 dB
}

// Noise-floor tracker constants.
//
// The floor falls fast and rises slowly, which is what makes the tracker
// usable from the first second: if arming happens to catch the room mid-noise
// (or mid-sentence) the seed is too high, and fast decay walks it back down to
// the true floor within a couple of chunks instead of leaving wake deaf for a
// minute. Rising slowly is the other half — a single door slam must not lift
// the bar out of speech's reach.
const (
	floorDecayAlpha = 0.5  // toward a quieter measurement
	floorRiseAlpha  = 0.05 // toward a louder one

	// floorRiseGate is how far above the floor a chunk may sit and still be
	// taken for the room on sight.
	//
	// This gate is load-bearing and was measured the hard way. Without it,
	// every non-waking chunk taught the floor — including a chunk that WAS
	// someone speaking and merely fell short of the bar. A live run of "hey
	// helix" at 2.2x the floor walked the bar from 0.00936 to 0.01065 over
	// three chunks: each failed wake raised the price of the next one, so a
	// speaker who was slightly too quiet got quieter relative to the bar the
	// harder they tried. A wake detector must never be harder to wake because
	// someone tried to wake it.
	floorRiseGate = 1.5

	// floorRiseSustain is how many consecutive above-the-gate chunks it takes
	// before a louder level is accepted as the new room.
	//
	// The gate above would otherwise leave the floor stuck when the room
	// genuinely changes — a fan spinning up moved this machine's floor from
	// 0.0011 to 0.0035, and a floor that cannot follow that would false-fire
	// on the fan forever. The two cases are told apart by duration, which is
	// the only thing that actually separates them: an utterance is one or two
	// chunks, a fan is every chunk. Four at the shipped 1.5s chunk is ~6s.
	floorRiseSustain = 4

	// floorFloor keeps a digitally-silent input from collapsing the bar to
	// zero. MinRMS is the real guard; this only bounds the arithmetic.
	floorFloor = 0.0002

	// floorCeiling bounds the bar in a room louder than speech. Past this the
	// energy engine is out of its depth (that is what the sidecar engine is
	// for) and the honest failure is "you have to raise your voice", not "wake
	// is mathematically unreachable".
	floorCeiling = 0.05
)

// EnergyDetector wakes when a chunk's normalized RMS exceeds both the
// absolute audibility gate and a multiple of the room noise floor it has
// measured so far. Safe for one scan loop; Wake and Bar take a lock so
// instrumentation can read the bar it judged against.
type EnergyDetector struct {
	// Ratio is how far above the measured floor a chunk must sit.
	Ratio float64

	// MinRMS is the absolute audibility gate — the level below which a clip
	// is treated as a dead or muted microphone rather than a quiet room.
	MinRMS float64

	mu        sync.Mutex
	floor     float64 // 0 until the first chunk seeds it
	bar       float64 // the level the last chunk was judged against
	sustained int     // consecutive above-gate, below-bar chunks
}

// NewEnergyDetector builds a detector from a sensitivity preset (defaults to
// balanced for unknown presets).
//
// HELIX_WAKE_RATIO and HELIX_WAKE_MIN_RMS override the two numbers, because a
// room this heuristic is wrong about should be fixable without a rebuild — and
// because /mictest reports the same RMS scale these are set on.
func NewEnergyDetector(preset Preset) *EnergyDetector {
	r, ok := PresetRatios[preset]
	if !ok {
		r = PresetRatios[PresetBalanced]
	}
	d := &EnergyDetector{Ratio: r, MinRMS: speech.SpeechRMSFloor}
	if v, ok := envFloat("HELIX_WAKE_RATIO", 1.05, 100); ok {
		d.Ratio = v
	}
	if v, ok := envFloat("HELIX_WAKE_MIN_RMS", 0, 1); ok {
		d.MinRMS = v
	}
	return d
}

// envFloat reads a clamped float override, reporting whether it applied.
func envFloat(key string, lo, hi float64) (float64, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || f < lo || f > hi {
		return 0, false
	}
	return f, true
}

// Bar reports the level the most recent chunk was compared against, for
// instrumentation. Zero before the first chunk.
func (d *EnergyDetector) Bar() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bar
}

// Floor reports the measured room noise floor, for instrumentation.
func (d *EnergyDetector) Floor() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.floor
}

// Wake scores one chunk: score is the chunk's normalized RMS (0..1); woke is
// true when it clears both the audibility gate and the floor multiple.
func (d *EnergyDetector) Wake(clip speech.AudioFormat) (score float64, woke bool, err error) {
	score, err = RMS(clip)
	if err != nil {
		return 0, false, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.floor == 0 {
		// The first chunk defines the room rather than waking on it: arming
		// happens at an idle prompt, so the opening 1.5s is the room by
		// construction, and fast decay repairs the seed if it was not.
		d.floor = clampFloor(score)
		d.bar = d.wakeBarLocked()
		return score, false, nil
	}

	d.bar = d.wakeBarLocked()
	woke = score >= d.bar

	d.learnLocked(score, woke)
	return score, woke, nil
}

// learnLocked folds one chunk into the room-noise estimate. Caller holds the
// lock.
//
// Three cases, and the middle one is the whole design:
//
//   - Quieter than the floor: follow it down fast. A room only gets quieter
//     by actually being quieter, so there is nothing to be careful about.
//   - Plainly the room (within floorRiseGate): follow it up slowly.
//   - Louder than that but not a wake: ambiguous — a near-miss utterance or a
//     room that just got louder. Believed only if it PERSISTS, so a spoken
//     phrase cannot raise the bar against the next attempt.
//
// A chunk that woke teaches nothing: the room is not what just happened.
func (d *EnergyDetector) learnLocked(score float64, woke bool) {
	if woke {
		d.sustained = 0
		return
	}
	switch {
	case score < d.floor:
		d.sustained = 0
		d.floor = clampFloor(d.floor + floorDecayAlpha*(score-d.floor))
	case score < d.floor*floorRiseGate:
		d.sustained = 0
		d.floor = clampFloor(d.floor + floorRiseAlpha*(score-d.floor))
	default:
		d.sustained++
		if d.sustained >= floorRiseSustain {
			d.floor = clampFloor(d.floor + floorRiseAlpha*(score-d.floor))
		}
	}
}

// wakeBarLocked is the level a chunk must clear. Caller holds the lock.
func (d *EnergyDetector) wakeBarLocked() float64 {
	bar := d.floor * d.Ratio
	if bar < d.MinRMS {
		bar = d.MinRMS
	}
	return bar
}

// clampFloor bounds a noise-floor estimate — see floorFloor/floorCeiling.
func clampFloor(v float64) float64 {
	if v < floorFloor {
		return floorFloor
	}
	if v > floorCeiling {
		return floorCeiling
	}
	return v
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

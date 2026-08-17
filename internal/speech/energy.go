// internal/speech/energy.go
// Purpose: Cheap amplitude analysis for the voice loop. "No speech detected"
// must be distinguishable from a transcription failure so the interactive loop
// can re-arm the microphone with a gentle prompt instead of silently dumping
// the user into typed fallback — the #1 source of "empty transcript" confusion.
package speech

import (
	"errors"
	"math"
)

// ErrNoSpeech is returned when a captured clip contains no audible content
// (below the RMS floor) — e.g. a dead mic, a too-quiet room, or the user
// speaking before the recorder armed. The voice loop retries on this.
var ErrNoSpeech = errors.New("no speech detected")

// ErrEmptyTranscript is returned when a provider transcribed silence (empty
// text). Also retryable — the provider heard audio but found no words.
var ErrEmptyTranscript = errors.New("empty transcript")

// speechRMSFloor is the default "something audible" level: ≈ −54 dBFS.
// Digital silence is 0; quiet-but-real speech is typically ≳ 0.01 RMS, so
// this floor rejects dead-mic/silence clips without rejecting soft speech.
const speechRMSFloor = 0.002

// ClipRMS returns the root-mean-square level of a WAV clip normalized to
// [0,1] (relative to 16-bit full scale). Returns 0 for undecodable or empty
// buffers so callers never have to special-case malformed audio.
func ClipRMS(audio AudioFormat) float64 {
	samples, err := DecodeWAVMono(audio.Bytes)
	if err != nil || len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// HasSpeech reports whether a clip has audible content above the given RMS
// floor (0..1). A non-positive floor selects the package default.
func HasSpeech(audio AudioFormat, minRMS float64) bool {
	if minRMS <= 0 {
		minRMS = speechRMSFloor
	}
	return ClipRMS(audio) >= minRMS
}

// ClipDuration returns the decoded duration of a WAV clip in seconds
// (0 for undecodable/empty buffers). Used to tell the user how much the mic
// actually captured — instant feedback that the recorder is alive.
func ClipDuration(audio AudioFormat) float64 {
	samples, err := DecodeWAVMono(audio.Bytes)
	if err != nil || len(samples) == 0 || audio.SampleRate <= 0 {
		return 0
	}
	return float64(len(samples)) / float64(audio.SampleRate)
}

// Level-meter constants for the voice HUD (BlackBox P12.4).
const (
	// levelFloorDB is the quietest level the meter shows movement for. Speech
	// RMS lives roughly between −50 dBFS (a quiet room) and −10 dBFS (close
	// talking), so the meter is mapped across that span rather than across the
	// full linear 0..1 range — a linear meter would sit pinned near zero for
	// all normal speech and look dead.
	levelFloorDB = -50.0

	// levelCeilDB is where the meter reads full scale.
	levelCeilDB = -10.0
)

// ClipLevel converts a clip's RMS into a 0..1 meter reading for the voice HUD.
//
// The mapping is logarithmic (dBFS), not linear, because human loudness
// perception is: a linear meter driven by RMS barely leaves the floor for
// ordinary speech, which is exactly the "dead-looking waveform" this replaces.
//
// Args:
//   - audio: a captured WAV chunk.
//
// Returns: 0 for silence or undecodable audio, 1 at levelCeilDB or louder.
// Complexity: O(samples).
func ClipLevel(audio AudioFormat) float64 {
	return levelFromRMS(ClipRMS(audio))
}

// levelFromRMS maps a linear RMS value onto the meter's dB span.
func levelFromRMS(rms float64) float64 {
	if rms <= 0 {
		return 0
	}
	db := 20 * math.Log10(rms)
	if db <= levelFloorDB {
		return 0
	}
	if db >= levelCeilDB {
		return 1
	}
	return (db - levelFloorDB) / (levelCeilDB - levelFloorDB)
}

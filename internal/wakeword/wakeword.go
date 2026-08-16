// Package wakeword provides continuous "Hey Helix" activation detection.
//
// The engine runs as an external sidecar service (ADR-002): a small local
// HTTP service (openWakeWord-class scorer) that receives audio chunks and
// returns per-phrase scores. The Helix core stays CGO-free; the sidecar is
// consent-gated and version/checksum-pinned at install time (threat V8).
//
// Security notes (docs/threat_model_voice.md V2): detection sensitivity,
// debounce/cooldown, and the hard kill switch ("stop listening", /voice off)
// are policy controls, not tuning knobs only.
//
// BlackBox Phase 3 (roadmap §6). Skeleton compiled and tested since Phase 0.
package wakeword

import (
	"context"
	"time"
)

// Sensitivity presets tune the detection threshold. Higher sensitivity means
// more true accepts at the cost of more false positives.
type Preset string

const (
	PresetStrict    Preset = "strict"
	PresetBalanced  Preset = "balanced"
	PresetLoose     Preset = "loose"
	CooldownDefault        = 2 * time.Second
)

// WakeEvent marks a wake-word detection.
type WakeEvent struct {
	// DetectedAt is when the scorer crossed the threshold.
	DetectedAt time.Time

	// Score is the raw scorer confidence at trigger time (0..1).
	Score float64

	// Phrase is the configured wake phrase (e.g. "hey helix").
	Phrase string
}

// Service continuously consumes audio chunks and emits wake events.
// Implemented in Phase 3 against the sidecar scorer; the interface is fixed
// now so the daemon and voice source can be built against it.
type Service interface {
	// Start begins detection; events fires at most once per cooldown window.
	Start(ctx context.Context) (<-chan WakeEvent, error)

	// Stop halts detection and releases the audio stream.
	Stop() error
}

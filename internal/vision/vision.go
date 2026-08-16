// Package vision provides BlackBox Phase 5: opt-in camera perception.
// A single frame is captured per conversational turn (ffmpeg shell-out, ADR-003),
// kept MEMORY-ONLY (never written to disk — enforced by fs-snapshot tests),
// and attached to a multimodal planner request.
//
// Privacy controls (docs/threat_model_voice.md V4): strict opt-in via /eyes on
// (default OFF), TTS announcement on activation, journal entry per frame batch
// (metadata only, never pixels), immediate deactivation via /eyes off or the
// voice phrase "turn off your eyes".
//
// Skeleton compiled and tested since Phase 0.
package vision

import "time"

// Frame is one captured camera frame. Bytes never touch the filesystem.
type Frame struct {
	CapturedAt time.Time
	// JPEG-encoded image bytes, downscaled to ≤1024px, quality ~80.
	Data []byte
	// SourceDevice identifies the capture device for journaling.
	SourceDevice string
}

// Config controls the capture service.
type Config struct {
	// Enabled is the master opt-in switch (default false).
	Enabled bool
	// MaxFramesPerTurn bounds captures per input event (default 1).
	MaxFramesPerTurn int
	// Provider names the configured vision LLM (nil → use active chat
	// provider if vision-capable).
	Provider string
}

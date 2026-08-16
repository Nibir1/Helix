// Package input abstracts Helix's input sources (TTY typing, voice capture,
// and later a hybrid of both) behind a single event channel, so the REPL loop
// and the future daemon can accept text from any origin with provenance
// attached.
//
// Provenance is load-bearing: the Voice Risk Policy (ADR-005,
// docs/threat_model_voice.md) caps the authority of anything arriving on
// ChannelVoice, so the Channel field must never be spoofable by input text
// itself.
//
// BlackBox Phase 2 (roadmap §6). Skeleton compiled and tested since Phase 0.
package input

import "context"

// Channel identifies where an input event originated.
type Channel string

const (
	// ChannelText is the classical typed terminal line: full authority.
	ChannelText Channel = "text"

	// ChannelVoice is a transcribed utterance: reduced authority under the
	// Voice Risk Policy (Medium-risk cap, fail-closed confirmations).
	ChannelVoice Channel = "voice"
)

// Valid reports whether c is a known channel value.
func (c Channel) Valid() bool {
	return c == ChannelText || c == ChannelVoice
}

// InputEvent is one unit of user input regardless of origin.
//
// Meta carries advisory metadata only (never authority): e.g.
// {"stt_confidence": 0.93, "stt_provider": "openai"} for voice events.
type InputEvent struct {
	Text    string
	Channel Channel
	Meta    map[string]any
}

// Source produces input events until closed or the context is cancelled.
// Implementations: TTYSource (Phase 2), VoiceSource (Phase 2), HybridSource
// (Phase 7).
type Source interface {
	// Events starts the source and returns its event stream. The channel
	// closes when ctx is cancelled or Close is called.
	Events(ctx context.Context) (<-chan InputEvent, error)

	// Close releases any resources (raw-mode state, recorder processes).
	Close() error
}

// internal/speech/types.go
// Purpose: Core contracts for the BlackBox speech layer — multi-provider
// speech-to-text (STT) and text-to-speech (TTS) behind unified interfaces,
// mirroring the proven internal/providers pattern (ADR-001/006; roadmap §6
// Phase 1).
package speech

import (
	"context"
	"io"
)

// AudioKind identifies the container/encoding of synthesized or captured audio.
type AudioKind string

const (
	// KindWAV is a RIFF/WAVE container (preferred for playback: pure-Go decode).
	KindWAV AudioKind = "wav"
	// KindPCM is headerless 16-bit little-endian PCM at a known sample rate.
	KindPCM AudioKind = "pcm"
	// KindMP3 is accepted in AudioFormat but playback support is deferred
	// (providers are configured to return WAV/PCM; see roadmap §13 open Qs).
	KindMP3 AudioKind = "mp3"
)

// AudioFormat carries one audio payload plus the metadata needed to decode or
// re-encode it for a provider.
type AudioFormat struct {
	Kind       AudioKind `json:"kind"`
	SampleRate int       `json:"sample_rate"`
	Channels   int       `json:"channels"`
	Bytes      []byte    `json:"-"`
}

// Transcript is the result of transcribing one audio clip.
type Transcript struct {
	Text       string  `json:"text"`
	Language   string  `json:"language,omitempty"`
	Confidence float64 `json:"confidence,omitempty"` // 0..1; 0 = unknown
	Provider   string  `json:"provider"`
	// IsFinal marks a streaming transcript as the utterance-final result;
	// false = interim partial (still speaking). Batch Transcribe always
	// yields IsFinal=true.
	IsFinal bool `json:"is_final,omitempty"`
}

// SynthesisOptions tune one text-to-speech request.
type SynthesisOptions struct {
	Voice string  // provider-specific voice id/name; empty = provider default
	Speed float64 // 1.0 = normal; 0 = provider default

	// Context is the recent conversation, oldest first, for providers whose
	// prosody is conditioned on it (CSM-1B). Every other adapter ignores it,
	// which is why this rides the options struct rather than the interface:
	// adding a parameter to TTSProvider.Synthesize would have touched six
	// adapters to serve one.
	//
	// Empty means single-utterance synthesis — the behavior of every provider
	// before CSM arrived, and still the default.
	Context []ConversationTurn
}

// STTProvider is the unified speech-to-text contract. Adapters exist for each
// vendor (cloud) and for local sidecar services (whisper.cpp over HTTP).
type STTProvider interface {
	Name() string
	DisplayName() string
	Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error)
	SetAPIKey(key string)
	HealthCheck(ctx context.Context) error
	RequiresAPIKey() bool
	IsLocal() bool
	DefaultModel() string
}

// TTSProvider is the unified text-to-speech contract. Adapters return WAV or
// PCM (never MP3) so playback stays dependency-free.
type TTSProvider interface {
	Name() string
	DisplayName() string
	Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error)
	SetAPIKey(key string)
	HealthCheck(ctx context.Context) error
	RequiresAPIKey() bool
	IsLocal() bool
	DefaultModel() string
}

// StreamedAudio is an in-flight synthesis: raw PCM arriving over the wire.
//
// Raw PCM, not WAV, by design — the streaming path requests a headerless
// format so there is no container to parse incrementally, and the sample rate
// is known from the provider contract rather than from bytes that may not have
// arrived yet.
type StreamedAudio struct {
	SampleRate int
	Channels   int
	Body       io.ReadCloser
}

// StreamingTTSProvider is implemented by adapters that can deliver audio while
// it is still being generated (BlackBox P7.2c). Optional, exactly like
// StreamingSTTProvider: the registry type-asserts for it and falls back to the
// buffered Synthesize when it is absent or fails before the first byte.
type StreamingTTSProvider interface {
	TTSProvider
	// SynthesizeStream begins synthesis and returns the audio body. The caller
	// owns Body and must close it.
	SynthesizeStream(ctx context.Context, text string, opts SynthesisOptions) (StreamedAudio, error)
}

// StreamingSTTProvider is implemented by adapters that support real-time
// streaming transcription (Deepgram WebSocket, OpenAI Realtime). The batch
// Transcribe path is sufficient for Phase 1-2; streaming lands with the
// hands-free loop (Phase 3).
type StreamingSTTProvider interface {
	STTProvider
	// Stream consumes chunked audio and emits partial/final transcripts.
	Stream(ctx context.Context, chunks <-chan AudioFormat) (<-chan Transcript, error)
}

// internal/speech/adapter_openai_tts.go
// Purpose: OpenAI text-to-speech adapter. Always requests WAV output so
// playback stays pure-Go (no MP3 decode dependency; ADR-007).
package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const openaiDefaultTTSModel = "tts-1"

// openaiTTS implements TTSProvider against POST /audio/speech.
type openaiTTS struct {
	name    string
	display string
	baseURL string
	model   string
	voice   string
	key     string
}

// NewOpenAITTS builds the cloud OpenAI TTS adapter.
func NewOpenAITTS(model, voice, baseURL string) TTSProvider {
	if model == "" {
		model = openaiDefaultTTSModel
	}
	if voice == "" {
		voice = "alloy"
	}
	if baseURL == "" {
		baseURL = openaiDefaultSTTBaseURL
	}
	return &openaiTTS{name: "openai", display: "OpenAI TTS", baseURL: baseURL, model: model, voice: voice}
}

func (p *openaiTTS) Name() string         { return p.name }
func (p *openaiTTS) DisplayName() string  { return p.display }
func (p *openaiTTS) SetAPIKey(key string) { p.key = key }
func (p *openaiTTS) RequiresAPIKey() bool { return true }
func (p *openaiTTS) IsLocal() bool        { return false }
func (p *openaiTTS) DefaultModel() string { return p.model }

// Synthesize returns WAV bytes for the text.
func (p *openaiTTS) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
	if p.key == "" {
		return AudioFormat{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if text == "" {
		return AudioFormat{}, fmt.Errorf("%s: empty text", p.name)
	}

	voice := p.voice
	if opts.Voice != "" {
		voice = opts.Voice
	}
	speed := opts.Speed
	if speed <= 0 {
		speed = 1.0
	}

	payload, err := json.Marshal(map[string]any{
		"model":           p.model,
		"input":           text,
		"voice":           voice,
		"speed":           speed,
		"response_format": "wav",
	})
	if err != nil {
		return AudioFormat{}, err
	}

	headers := map[string]string{"Authorization": "Bearer " + p.key}
	data, err := sharedClient.DoRaw(ctx, http.MethodPost,
		p.baseURL+"/audio/speech", headers, "application/json", payload)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	if len(data) < 44 || string(data[:4]) != "RIFF" {
		return AudioFormat{}, fmt.Errorf("%s: unexpected non-WAV response (%d bytes)", p.name, len(data))
	}

	rate, channels, err := wavHeaderInfo(data)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	return AudioFormat{Kind: KindWAV, SampleRate: rate, Channels: channels, Bytes: data}, nil
}

// HealthCheck verifies the key against /models.
func (p *openaiTTS) HealthCheck(ctx context.Context) error {
	data, err := sharedClient.DoRaw(ctx, http.MethodGet, p.baseURL+"/models",
		map[string]string{"Authorization": "Bearer " + p.key}, "", nil)
	if err != nil {
		return fmt.Errorf("%s: %w", p.name, err)
	}
	_ = data
	return nil
}

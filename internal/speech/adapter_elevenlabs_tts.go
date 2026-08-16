// internal/speech/adapter_elevenlabs_tts.go
// Purpose: ElevenLabs TTS adapter. Requests raw PCM (headerless 16-bit LE) —
// never MP3 — so playback stays pure-Go (ADR-007).
package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	elevenlabsDefaultBaseURL = "https://api.elevenlabs.io/v1"
	elevenlabsDefaultVoice   = "21m00Tcm4TlvDq8ikWAM" // "Rachel"
	elevenlabsDefaultModel   = "eleven_turbo_v2_5"
	elevenlabsPCMRate        = 24000
)

// elevenlabsTTS implements TTSProvider against POST /text-to-speech/{voice}.
type elevenlabsTTS struct {
	name    string
	display string
	baseURL string
	model   string
	voice   string
	key     string
}

// NewElevenLabsTTS builds the ElevenLabs adapter.
func NewElevenLabsTTS(model, voice, baseURL string) TTSProvider {
	if model == "" {
		model = elevenlabsDefaultModel
	}
	if voice == "" {
		voice = elevenlabsDefaultVoice
	}
	if baseURL == "" {
		baseURL = elevenlabsDefaultBaseURL
	}
	return &elevenlabsTTS{name: "elevenlabs", display: "ElevenLabs", baseURL: baseURL, model: model, voice: voice}
}

func (p *elevenlabsTTS) Name() string         { return p.name }
func (p *elevenlabsTTS) DisplayName() string  { return p.display }
func (p *elevenlabsTTS) SetAPIKey(key string) { p.key = key }
func (p *elevenlabsTTS) RequiresAPIKey() bool { return true }
func (p *elevenlabsTTS) IsLocal() bool        { return false }
func (p *elevenlabsTTS) DefaultModel() string { return p.model }

// Synthesize returns 24kHz mono PCM for the text.
func (p *elevenlabsTTS) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
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

	payload, err := json.Marshal(map[string]any{
		"text":     text,
		"model_id": p.model,
	})
	if err != nil {
		return AudioFormat{}, err
	}

	url := fmt.Sprintf("%s/text-to-speech/%s?output_format=pcm_%d", p.baseURL, voice, elevenlabsPCMRate)
	headers := map[string]string{"xi-api-key": p.key}
	data, err := sharedClient.DoRaw(ctx, http.MethodPost, url, headers, "application/json", payload)
	if err != nil {
		return AudioFormat{}, fmt.Errorf("%s: %w", p.name, err)
	}
	if len(data) == 0 {
		return AudioFormat{}, fmt.Errorf("%s: empty audio response", p.name)
	}

	return AudioFormat{Kind: KindPCM, SampleRate: elevenlabsPCMRate, Channels: 1, Bytes: data}, nil
}

// HealthCheck verifies the key against GET /user.
func (p *elevenlabsTTS) HealthCheck(ctx context.Context) error {
	_, err := sharedClient.DoRaw(ctx, http.MethodGet, p.baseURL+"/user",
		map[string]string{"xi-api-key": p.key}, "", nil)
	if err != nil {
		return fmt.Errorf("%s: %w", p.name, err)
	}
	return nil
}

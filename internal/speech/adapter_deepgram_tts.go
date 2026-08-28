// internal/speech/adapter_deepgram_tts.go
// Purpose: Deepgram Aura-2 text-to-speech adapter. Requests linear16 WAV so
// playback stays pure-Go (ADR-007). Aura-2 pairs sub-300ms first-byte latency
// with $30/1M-char pricing — the low-latency cloud TTS option alongside the
// cheaper gpt-4o-mini-tts default.
package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const auraDefaultModel = "aura-2-thalia-en"

// deepgramTTS implements TTSProvider against POST /speak.
type deepgramTTS struct {
	name    string
	display string
	baseURL string
	model   string
	key     string
}

// NewDeepgramTTS builds the Deepgram Aura-2 adapter. The Aura voice is chosen
// by model id (e.g. aura-2-thalia-en); SynthesisOptions.Voice overrides it.
func NewDeepgramTTS(model, baseURL string) TTSProvider {
	if model == "" {
		model = auraDefaultModel
	}
	if baseURL == "" {
		baseURL = deepgramDefaultBaseURL
	}
	return &deepgramTTS{name: "deepgram", display: "Deepgram Aura-2", baseURL: baseURL, model: model}
}

func (p *deepgramTTS) Name() string         { return p.name }
func (p *deepgramTTS) DisplayName() string  { return p.display }
func (p *deepgramTTS) SetAPIKey(key string) { p.key = key }
func (p *deepgramTTS) RequiresAPIKey() bool { return true }
func (p *deepgramTTS) IsLocal() bool        { return false }
func (p *deepgramTTS) DefaultModel() string { return p.model }

// Synthesize returns 24 kHz mono WAV bytes for the text.
func (p *deepgramTTS) Synthesize(ctx context.Context, text string, opts SynthesisOptions) (AudioFormat, error) {
	if p.key == "" {
		return AudioFormat{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if text == "" {
		return AudioFormat{}, fmt.Errorf("%s: empty text", p.name)
	}

	model := p.model
	if opts.Voice != "" {
		model = opts.Voice
	}
	// container=wav + linear16 keeps the pure-Go decode path (no MP3).
	url := fmt.Sprintf("%s/speak?model=%s&encoding=linear16&sample_rate=24000&container=wav",
		p.baseURL, model)
	headers := map[string]string{"Authorization": "Token " + p.key}
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return AudioFormat{}, err
	}

	data, err := sharedClient.DoRaw(ctx, http.MethodPost, url, headers, "application/json", payload)
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

// HealthCheck verifies the key against the projects endpoint.
func (p *deepgramTTS) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/projects", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+p.key)
	resp, err := sharedClient.RawClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable: %w", p.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s health: HTTP %d", p.name, resp.StatusCode)
	}
	return nil
}

// internal/speech/adapter_deepgram_stt.go
// Purpose: Deepgram STT adapter (nova-2). Uses the batch REST endpoint —
// already very low latency — with WAV bytes in the body. WebSocket streaming
// (StreamingSTTProvider) lands with the hands-free loop (Phase 3).
package speech

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	deepgramDefaultBaseURL = "https://api.deepgram.com/v1"
	deepgramDefaultModel   = "nova-2"
)

// deepgramSTT implements STTProvider against POST /listen.
type deepgramSTT struct {
	name    string
	display string
	baseURL string
	model   string
	key     string
}

// NewDeepgramSTT builds the Deepgram adapter.
func NewDeepgramSTT(model, baseURL string) STTProvider {
	if model == "" {
		model = deepgramDefaultModel
	}
	if baseURL == "" {
		baseURL = deepgramDefaultBaseURL
	}
	return &deepgramSTT{name: "deepgram", display: "Deepgram", baseURL: baseURL, model: model}
}

func (p *deepgramSTT) Name() string         { return p.name }
func (p *deepgramSTT) DisplayName() string  { return p.display }
func (p *deepgramSTT) SetAPIKey(key string) { p.key = key }
func (p *deepgramSTT) RequiresAPIKey() bool { return true }
func (p *deepgramSTT) IsLocal() bool        { return false }
func (p *deepgramSTT) DefaultModel() string { return p.model }

// Transcribe sends raw WAV bytes and parses the channel/alternative result.
func (p *deepgramSTT) Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error) {
	if p.key == "" {
		return Transcript{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if len(audio.Bytes) == 0 {
		return Transcript{}, fmt.Errorf("%s: empty audio clip", p.name)
	}

	url := p.baseURL + "/listen?model=" + p.model + "&smart_format=true&punctuate=true"
	headers := map[string]string{"Authorization": "Token " + p.key}
	data, err := sharedClient.DoRaw(ctx, http.MethodPost, url, headers, "audio/wav", audio.Bytes)
	if err != nil {
		return Transcript{}, fmt.Errorf("%s: %w", p.name, err)
	}

	var out struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string  `json:"transcript"`
					Confidence float64 `json:"confidence"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
		Metadata struct {
			Language string `json:"language"`
		} `json:"metadata,omitempty"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Transcript{}, fmt.Errorf("%s: parse response: %w", p.name, err)
	}

	if len(out.Results.Channels) == 0 || len(out.Results.Channels[0].Alternatives) == 0 {
		return Transcript{}, fmt.Errorf("%s: no transcription in response", p.name)
	}
	alt := out.Results.Channels[0].Alternatives[0]

	return Transcript{
		Text:       alt.Transcript,
		Language:   out.Metadata.Language,
		Confidence: alt.Confidence,
		Provider:   p.name,
	}, nil
}

// HealthCheck verifies the key against GET /projects.
func (p *deepgramSTT) HealthCheck(ctx context.Context) error {
	_, err := sharedClient.DoRaw(ctx, http.MethodGet, p.baseURL+"/projects",
		map[string]string{"Authorization": "Token " + p.key}, "", nil)
	if err != nil {
		return fmt.Errorf("%s: %w", p.name, err)
	}
	return nil
}

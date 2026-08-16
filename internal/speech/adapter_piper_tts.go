// internal/speech/adapter_piper_tts.go
// Purpose: Piper TTS sidecar adapter (rhasspy piper-http style service at
// /api/tts, returning WAV). Local, free, offline — the private TTS default
// (ADR-002).
package speech

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

const piperDefaultBaseURL = "http://127.0.0.1:5000"

// piperTTS implements TTSProvider against a local piper-http sidecar.
type piperTTS struct {
	name    string
	display string
	baseURL string
}

// NewPiperTTS builds the Piper sidecar adapter.
func NewPiperTTS(baseURL string) TTSProvider {
	if baseURL == "" {
		baseURL = piperDefaultBaseURL
	}
	return &piperTTS{name: "piper-local", display: "Piper (local sidecar)", baseURL: baseURL}
}

func (p *piperTTS) Name() string         { return p.name }
func (p *piperTTS) DisplayName() string  { return p.display }
func (p *piperTTS) SetAPIKey(string)     {} // local service: no key
func (p *piperTTS) RequiresAPIKey() bool { return false }
func (p *piperTTS) IsLocal() bool        { return true }
func (p *piperTTS) DefaultModel() string { return "piper" }

// Synthesize fetches WAV bytes for the text from the sidecar.
func (p *piperTTS) Synthesize(ctx context.Context, text string, _ SynthesisOptions) (AudioFormat, error) {
	if text == "" {
		return AudioFormat{}, fmt.Errorf("%s: empty text", p.name)
	}

	u := p.baseURL + "/api/tts?" + url.Values{"text": {text}}.Encode()
	data, err := sharedClient.DoRaw(ctx, http.MethodGet, u, nil, "", nil)
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

// HealthCheck treats any HTTP response as proof the sidecar is alive.
func (p *piperTTS) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/", nil)
	if err != nil {
		return err
	}
	resp, err := sharedClient.RawClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable: %w", p.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

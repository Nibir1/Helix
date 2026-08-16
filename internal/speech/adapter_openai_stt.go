// internal/speech/adapter_openai_stt.go
// Purpose: OpenAI-compatible STT adapter. Serves double duty: the "openai"
// provider (Whisper API) and the "whisper-local" provider (whisper.cpp server
// exposing the same /v1/audio/transcriptions contract over HTTP — ADR-002
// sidecar pattern, no API key, local base URL).
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
)

const (
	openaiDefaultSTTBaseURL = "https://api.openai.com/v1"
	whisperLocalDefaultURL  = "http://127.0.0.1:8080/v1"
	openaiDefaultSTTModel   = "whisper-1"
)

// openaiSTT implements STTProvider against any OpenAI-compatible audio
// transcription endpoint.
type openaiSTT struct {
	name    string
	display string
	baseURL string
	model   string
	key     string
	local   bool
}

// NewOpenAISTT builds the cloud OpenAI Whisper adapter.
func NewOpenAISTT(model, baseURL string) STTProvider {
	if model == "" {
		model = openaiDefaultSTTModel
	}
	if baseURL == "" {
		baseURL = openaiDefaultSTTBaseURL
	}
	return &openaiSTT{
		name:    "openai",
		display: "OpenAI Whisper",
		baseURL: baseURL,
		model:   model,
		local:   false,
	}
}

// NewWhisperLocalSTT builds the local whisper.cpp sidecar adapter.
func NewWhisperLocalSTT(model, baseURL string) STTProvider {
	if baseURL == "" {
		baseURL = whisperLocalDefaultURL
	}
	return &openaiSTT{
		name:    "whisper-local",
		display: "Whisper (local sidecar)",
		baseURL: baseURL,
		model:   model, // empty = use the server's loaded model
		local:   true,
	}
}

func (p *openaiSTT) Name() string         { return p.name }
func (p *openaiSTT) DisplayName() string  { return p.display }
func (p *openaiSTT) SetAPIKey(key string) { p.key = key }
func (p *openaiSTT) RequiresAPIKey() bool { return !p.local }
func (p *openaiSTT) IsLocal() bool        { return p.local }
func (p *openaiSTT) DefaultModel() string { return p.model }

// Transcribe POSTs the clip as multipart/form-data to /audio/transcriptions.
func (p *openaiSTT) Transcribe(ctx context.Context, audio AudioFormat) (Transcript, error) {
	if p.RequiresAPIKey() && p.key == "" {
		return Transcript{}, fmt.Errorf("%s: missing API key", p.name)
	}
	if len(audio.Bytes) == 0 {
		return Transcript{}, fmt.Errorf("%s: empty audio clip", p.name)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", "clip."+string(audio.Kind))
	if err != nil {
		return Transcript{}, fmt.Errorf("%s: build form: %w", p.name, err)
	}
	if _, err := fw.Write(audio.Bytes); err != nil {
		return Transcript{}, fmt.Errorf("%s: write form: %w", p.name, err)
	}
	if p.model != "" {
		_ = w.WriteField("model", p.model)
	}
	if err := w.Close(); err != nil {
		return Transcript{}, fmt.Errorf("%s: close form: %w", p.name, err)
	}

	headers := map[string]string{}
	if p.key != "" {
		headers["Authorization"] = "Bearer " + p.key
	}
	data, err := sharedClient.DoRaw(ctx, http.MethodPost,
		p.baseURL+"/audio/transcriptions", headers, w.FormDataContentType(), body.Bytes())
	if err != nil {
		return Transcript{}, fmt.Errorf("%s: %w", p.name, err)
	}

	var out struct {
		Text     string `json:"text"`
		Language string `json:"language,omitempty"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return Transcript{}, fmt.Errorf("%s: parse response: %w", p.name, err)
	}

	return Transcript{
		Text:     out.Text,
		Language: out.Language,
		Provider: p.name,
	}, nil
}

// HealthCheck probes the endpoint. For the local sidecar ANY HTTP response —
// even 404 — proves reachability, since whisper.cpp server versions differ in
// which routes they expose.
func (p *openaiSTT) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	if p.key != "" {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	resp, err := sharedClient.RawClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s unreachable: %w", p.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if p.local && resp.StatusCode != http.StatusOK {
		// Reachable but not the OpenAI shape — still a live sidecar.
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s health: HTTP %d", p.name, resp.StatusCode)
	}
	return nil
}

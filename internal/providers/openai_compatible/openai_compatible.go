// internal/providers/openai_compatible/openai_compatible.go
// Purpose: Generic OpenAI-compatible provider.
package openaicompatible

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"helix/internal/providers"
)

// Config configures an OpenAI-compatible provider.
type Config struct {
	Name         string
	DisplayName  string
	BaseURL      string
	APIKey       string
	DefaultModel string
	Local        bool
}

// Provider is an OpenAI-compatible provider.
type Provider struct {
	cfg    Config
	apiKey string
	client *providers.HTTPClient
}

// New creates an OpenAI-compatible provider.
func New(cfg Config, client *providers.HTTPClient) *Provider {
	return &Provider{
		cfg:    cfg,
		apiKey: cfg.APIKey,
		client: client,
	}
}

func (p *Provider) Name() string {
	return p.cfg.Name
}

func (p *Provider) DisplayName() string {
	return p.cfg.DisplayName
}

func (p *Provider) SetAPIKey(key string) {
	p.apiKey = strings.TrimSpace(key)
}

func (p *Provider) RequiresAPIKey() bool {
	return !p.cfg.Local
}

func (p *Provider) IsLocal() bool {
	return p.cfg.Local
}

func (p *Provider) DefaultModel() string {
	return p.cfg.DefaultModel
}

func (p *Provider) Capabilities() providers.Capabilities {
	return providers.CapabilitiesFor(p.cfg.Name, p.cfg.DefaultModel)
}

// Chat sends an OpenAI-compatible streaming chat request.
func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	if p.RequiresAPIKey() && p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured for %s", p.Name())
	}

	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}

	model := req.Model
	if model == "" {
		model = p.DefaultModel()
	}

	body := map[string]interface{}{
		"model":    model,
		"messages": toWireMessages(req.Messages),
		"stream":   true,
	}

	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}

	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}

	// Native tool calling (P8.7). Only sent when the caller supplied tools, so
	// the wire format for ordinary chat is byte-identical to before.
	if len(req.Tools) > 0 {
		body["tools"] = providers.ToolsToOpenAIWire(req.Tools)
		if req.ToolChoice != "" {
			body["tool_choice"] = req.ToolChoice
		}
	}

	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	url := strings.TrimSuffix(p.cfg.BaseURL, "/") + "/chat/completions"

	return p.client.DoStream(ctx, url, headers, body)
}

// toWireMessages converts normalized ChatMessages to the OpenAI wire format.
// Text-only messages keep the flat {"role","content"} shape; messages carrying
// multimodal Parts become the content-array form (BlackBox Phase 5).
func toWireMessages(messages []providers.ChatMessage) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, m := range messages {
		if len(m.Parts) == 0 {
			out = append(out, map[string]any{"role": m.Role, "content": m.Content})
			continue
		}

		content := make([]map[string]any, 0, len(m.Parts)+1)
		if m.Content != "" {
			content = append(content, map[string]any{"type": "text", "text": m.Content})
		}
		for _, p := range m.Parts {
			switch p.Type {
			case providers.PartText:
				content = append(content, map[string]any{"type": "text", "text": p.Text})
			case providers.PartImageURL:
				content = append(content, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": p.ImageURL},
				})
			case providers.PartImage:
				b64 := base64.StdEncoding.EncodeToString(p.ImageData)
				content = append(content, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": "data:image/jpeg;base64," + b64},
				})
			}
		}
		out = append(out, map[string]any{"role": m.Role, "content": content})
	}
	return out
}

// ListModels fetches models from /models.
func (p *Provider) ListModels(ctx context.Context) ([]providers.ModelInfo, error) {
	if p.RequiresAPIKey() && p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured for %s", p.Name())
	}

	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	url := strings.TrimSuffix(p.cfg.BaseURL, "/") + "/models"

	data, err := p.client.DoJSON(ctx, "GET", url, headers, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	if err := json.Unmarshal(data, &parsed); err == nil && len(parsed.Data) > 0 {
		models := make([]providers.ModelInfo, 0, len(parsed.Data))

		for _, m := range parsed.Data {
			models = append(models, providers.ModelInfo{
				ID:      m.ID,
				Name:    m.ID,
				OwnedBy: m.OwnedBy,
			})
		}

		return models, nil
	}

	var arr []struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	}

	if err := json.Unmarshal(data, &arr); err == nil {
		models := make([]providers.ModelInfo, 0, len(arr))

		for _, m := range arr {
			models = append(models, providers.ModelInfo{
				ID:      m.ID,
				Name:    m.ID,
				OwnedBy: m.OwnedBy,
			})
		}

		return models, nil
	}

	return nil, fmt.Errorf("unable to parse model list from %s", p.Name())
}

// HealthCheck verifies the provider by listing models.
func (p *Provider) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err := p.ListModels(ctx)
	return err
}

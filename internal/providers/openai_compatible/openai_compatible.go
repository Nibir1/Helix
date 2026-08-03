// internal/providers/openai_compatible/openai_compatible.go
// Purpose: Generic OpenAI-compatible provider.
package openaicompatible

import (
	"context"
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
		"messages": req.Messages,
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

	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}

	url := strings.TrimSuffix(p.cfg.BaseURL, "/") + "/chat/completions"

	return p.client.DoStream(ctx, url, headers, body)
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

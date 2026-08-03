// internal/providers/openai/openai.go
// Purpose: OpenAI provider with embeddings support.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

const (
	baseURL      = "https://api.openai.com/v1"
	defaultModel = "gpt-4o"
)

// Provider implements OpenAI.
type Provider struct {
	*openaicompatible.Provider
	apiKey string
	client *providers.HTTPClient
}

// New creates an OpenAI provider.
func New(apiKey string, client *providers.HTTPClient) *Provider {
	return &Provider{
		Provider: openaicompatible.New(openaicompatible.Config{
			Name:         "openai",
			DisplayName:  "OpenAI",
			BaseURL:      baseURL,
			APIKey:       apiKey,
			DefaultModel: defaultModel,
		}, client),
		apiKey: apiKey,
		client: client,
	}
}

// SetAPIKey updates the key in both wrapper and embedded provider.
func (p *Provider) SetAPIKey(key string) {
	p.apiKey = strings.TrimSpace(key)
	p.Provider.SetAPIKey(p.apiKey)
}

// Embed calls OpenAI embeddings.
func (p *Provider) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not configured")
	}

	if len(texts) == 0 {
		return [][]float32{}, nil
	}

	if model == "" {
		model = "text-embedding-3-small"
	}

	url := strings.TrimSuffix(baseURL, "/") + "/embeddings"

	body := map[string]interface{}{
		"model": model,
		"input": texts,
	}

	headers := map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}

	data, err := p.client.DoJSON(ctx, "POST", url, headers, body)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse embeddings response: %w", err)
	}

	out := make([][]float32, len(parsed.Data))
	for i, item := range parsed.Data {
		out[i] = item.Embedding
	}

	return out, nil
}

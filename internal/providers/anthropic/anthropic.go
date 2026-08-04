// internal/providers/anthropic/anthropic.go
// Purpose: Anthropic Claude provider.
package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"helix/internal/providers"
)

const (
	baseURL      = "https://api.anthropic.com"
	apiVersion   = "2023-06-01"
	defaultModel = "claude-opus-4-8"
)

// Provider implements Anthropic.
type Provider struct {
	apiKey string
	client *providers.HTTPClient
}

// New creates an Anthropic provider.
func New(apiKey string, client *providers.HTTPClient) *Provider {
	return &Provider{
		apiKey: apiKey,
		client: client,
	}
}

func (p *Provider) Name() string        { return "anthropic" }
func (p *Provider) DisplayName() string { return "Anthropic" }

func (p *Provider) SetAPIKey(key string) {
	p.apiKey = strings.TrimSpace(key)
}

func (p *Provider) RequiresAPIKey() bool { return true }
func (p *Provider) IsLocal() bool        { return false }
func (p *Provider) DefaultModel() string { return defaultModel }

func (p *Provider) Capabilities() providers.Capabilities {
	return providers.CapabilitiesFor("anthropic", defaultModel)
}

// Chat sends a streaming Anthropic Messages request.
func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("anthropic API key not configured")
	}

	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("no messages provided")
	}

	model := req.Model
	if model == "" {
		model = defaultModel
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4_096
	}

	messages, system := buildMessages(req.Messages)

	body := map[string]interface{}{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   messages,
		"stream":     true,
	}

	if system != "" {
		body["system"] = system
	}

	// Anthropic's latest models (Opus 4.x, Sonnet 4.x) strictly reject the `temperature`
	// parameter (returning 400 "deprecated for this model"). We omit it entirely and
	// rely on Anthropic's default (1.0), which is optimal for planner/chat tasks.

	headers := map[string]string{
		"x-api-key":         p.apiKey,
		"anthropic-version": apiVersion,
		"Accept":            "text/event-stream",
	}

	resp, err := p.client.DoRequest(ctx, "POST", baseURL+"/v1/messages", headers, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan providers.StreamChunk, 100)

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()
		parseAnthropicStream(resp.Body, ch)
	}()

	return ch, nil
}

// ListModels lists Anthropic models.
func (p *Provider) ListModels(ctx context.Context) ([]providers.ModelInfo, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("anthropic API key not configured")
	}

	headers := map[string]string{
		"x-api-key":         p.apiKey,
		"anthropic-version": apiVersion,
	}

	data, err := p.client.DoJSON(ctx, "GET", baseURL+"/v1/models", headers, nil)
	if err != nil {
		return nil, err
	}

	var parsed struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}

	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse Anthropic models: %w", err)
	}

	models := make([]providers.ModelInfo, 0, len(parsed.Data))

	for _, m := range parsed.Data {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}

		models = append(models, providers.ModelInfo{
			ID:   m.ID,
			Name: name,
		})
	}

	return models, nil
}

// HealthCheck verifies Anthropic by listing models.
func (p *Provider) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err := p.ListModels(ctx)
	return err
}

func buildMessages(messages []providers.ChatMessage) ([]map[string]string, string) {
	out := make([]map[string]string, 0, len(messages))
	system := ""

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			system = msg.Content
		case "user", "assistant":
			out = append(out, map[string]string{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	return out, system
}

func parseAnthropicStream(r io.Reader, ch chan<- providers.StreamChunk) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// FIX: Handle Anthropic stream errors explicitly to prevent hangs
		if event.Type == "error" {
			msg := event.Error.Message
			if msg == "" {
				msg = "unknown anthropic stream error"
			}
			ch <- providers.StreamChunk{Error: fmt.Errorf("anthropic error: %s", msg)}
			return
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			ch <- providers.StreamChunk{Content: event.Delta.Text}
		}

		if event.Type == "message_stop" {
			ch <- providers.StreamChunk{Done: true}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- providers.StreamChunk{Error: fmt.Errorf("anthropic stream error: %w", err)}
		return
	}

	// FIX: If stream ends without message_stop, send Done to prevent infinite hang
	ch <- providers.StreamChunk{Done: true}
}

// internal/providers/anthropic/anthropic.go
// Purpose: Anthropic Claude provider.
package anthropic

import (
	"bufio"
	"context"
	"encoding/base64"
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

	// endpoint overrides the API base URL. Empty means the real service; it
	// exists so the wire format can be tested against a stub server, which
	// matters now that the adapter builds tool definitions and reassembles
	// streamed tool_use blocks.
	endpoint string
}

// New creates an Anthropic provider.
func New(apiKey string, client *providers.HTTPClient) *Provider {
	return &Provider{
		apiKey: apiKey,
		client: client,
	}
}

// base returns the API base URL in force.
func (p *Provider) base() string {
	if p.endpoint != "" {
		return p.endpoint
	}
	return baseURL
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

	// Native tool calling (P8.7b). Anthropic's schema field is `input_schema`
	// (not OpenAI's nested `function.parameters`) and its "you must call a
	// tool" choice is spelled `{"type":"any"}` — hence the dedicated mapping
	// helpers rather than reusing the OpenAI envelope.
	if len(req.Tools) > 0 {
		body["tools"] = providers.ToolsToAnthropicWire(req.Tools)
		if choice := providers.AnthropicToolChoice(req.ToolChoice); choice != nil {
			body["tool_choice"] = choice
		}
	}

	// Anthropic's latest models (Opus 4.x, Sonnet 4.x) strictly reject the `temperature`
	// parameter (returning 400 "deprecated for this model"). We omit it entirely and
	// rely on Anthropic's default (1.0), which is optimal for planner/chat tasks.

	headers := map[string]string{
		"x-api-key":         p.apiKey,
		"anthropic-version": apiVersion,
		"Accept":            "text/event-stream",
	}

	resp, err := p.client.DoRequest(ctx, "POST", p.base()+"/v1/messages", headers, body)
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

	data, err := p.client.DoJSON(ctx, "GET", p.base()+"/v1/models", headers, nil)
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

func buildMessages(messages []providers.ChatMessage) ([]map[string]any, string) {
	out := make([]map[string]any, 0, len(messages))
	system := ""

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			system = msg.Content
		case "user", "assistant":
			// BlackBox Phase 5: vision blocks become a content array.
			if msg.HasImages() {
				blocks := make([]map[string]any, 0, len(msg.Parts)+1)
				if msg.Content != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": msg.Content})
				}
				for _, p := range msg.Parts {
					switch p.Type {
					case providers.PartText:
						blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
					case providers.PartImage:
						blocks = append(blocks, map[string]any{
							"type": "image",
							"source": map[string]any{
								"type":       "base64",
								"media_type": "image/jpeg",
								"data":       base64.StdEncoding.EncodeToString(p.ImageData),
							},
						})
					}
				}
				out = append(out, map[string]any{"role": msg.Role, "content": blocks})
				continue
			}
			out = append(out, map[string]any{
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

	// Tool calls arrive as `tool_use` content blocks (P8.7b): a
	// content_block_start carries the id and name at an index, then a run of
	// input_json_delta fragments builds the argument JSON. Both are keyed by
	// the block index, which is why the shared accumulator applies here even
	// though the event names differ entirely from OpenAI's.
	tools := providers.NewToolCallAccumulator()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		var event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
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

		switch {
		case event.Type == "content_block_start" && event.ContentBlock.Type == "tool_use":
			tools.Add(event.Index, event.ContentBlock.ID, event.ContentBlock.Name, "")

		case event.Type == "content_block_delta" && event.Delta.Type == "input_json_delta":
			tools.Add(event.Index, "", "", event.Delta.PartialJSON)

		case event.Type == "content_block_delta" && event.Delta.Type == "text_delta":
			ch <- providers.StreamChunk{Content: event.Delta.Text}
		}

		if event.Type == "message_stop" {
			ch <- providers.StreamChunk{Done: true, ToolCalls: tools.Assemble()}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- providers.StreamChunk{Error: fmt.Errorf("anthropic stream error: %w", err)}
		return
	}

	// FIX: If stream ends without message_stop, send Done to prevent infinite
	// hang — and still deliver any tool calls assembled so far.
	ch <- providers.StreamChunk{Done: true, ToolCalls: tools.Assemble()}
}

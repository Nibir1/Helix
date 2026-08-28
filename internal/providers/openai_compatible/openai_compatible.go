// internal/providers/openai_compatible/openai_compatible.go
// Purpose: Generic OpenAI-compatible provider.
package openaicompatible

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
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

	// tokenFields caches, per model, which completion-bound field the endpoint
	// accepts, learned from a rejection. Guarded because one provider instance
	// is shared across concurrent calls.
	tokenFieldMu sync.Mutex
	tokenFields  map[string]string

	// fixedTemperature records models that reject any temperature but their
	// default. OpenAI's reasoning models are sampled at a fixed temperature and
	// return 400 for anything else, including the value Helix has always sent —
	// so the planner failed on every single call until this was learned.
	fixedTempMu sync.Mutex
	fixedTemp   map[string]bool
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
//
// The completion bound is sent under whichever field this provider+model
// accepts. OpenAI's reasoning models (GPT-5.x, o1, o3, o4) reject max_tokens
// with an unsupported_parameter 400, while every other OpenAI-compatible server
// — llama.cpp, Ollama, Groq, DeepSeek, xAI — understands only max_tokens. There
// is no single field that works everywhere, so the first attempt uses a guess
// from the model name and a wrong guess is corrected from the server's own error
// and remembered. See tokenFieldFor.
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

	headers := map[string]string{}
	if p.apiKey != "" {
		headers["Authorization"] = "Bearer " + p.apiKey
	}
	url := strings.TrimSuffix(p.cfg.BaseURL, "/") + "/chat/completions"

	field := p.tokenFieldFor(model)
	ch, err := p.client.DoStream(ctx, url, headers,
		p.buildBody(req, model, field, p.temperatureAllowed(model)))
	if err == nil {
		return ch, nil
	}

	// The server may have told us which parameter it objected to. Believe it,
	// once per objection — a wrong field name and an unsupported temperature are
	// separate rejections and can both need correcting.
	if rejectsTemperature(err) {
		p.rememberFixedTemperature(model)
		ch, retryErr := p.client.DoStream(ctx, url, headers,
			p.buildBody(req, model, field, false))
		if retryErr == nil {
			return ch, nil
		}
		err = retryErr
	}

	alt, ok := correctedTokenField(err, field)
	if !ok {
		return nil, err
	}
	p.rememberTokenField(model, alt)
	return p.client.DoStream(ctx, url, headers,
		p.buildBody(req, model, alt, p.temperatureAllowed(model)))
}

// buildBody renders the request, placing the completion bound under tokenField.
func (p *Provider) buildBody(
	req providers.ChatRequest, model, tokenField string, withTemperature bool,
) map[string]interface{} {
	body := map[string]interface{}{
		"model":    model,
		"messages": toWireMessages(req.Messages),
		"stream":   true,
	}

	// Omitted entirely rather than sent as 1: "the default" is the server's to
	// define, and pinning a number here would be a second guess to get wrong.
	if req.Temperature != nil && withTemperature {
		body["temperature"] = *req.Temperature
	}

	if req.MaxTokens > 0 {
		body[tokenField] = req.MaxTokens
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
	return body
}

// tokenFieldFor returns the completion-bound field for a model: the learned
// value if this process has already been corrected, otherwise the name-based
// guess.
func (p *Provider) tokenFieldFor(model string) string {
	p.tokenFieldMu.Lock()
	learned, ok := p.tokenFields[model]
	p.tokenFieldMu.Unlock()
	if ok {
		return learned
	}
	return providers.PreferredMaxTokensField(p.cfg.Name, model)
}

// rememberTokenField caches a correction so it costs one extra round trip per
// model per process, not one per call.
func (p *Provider) rememberTokenField(model, field string) {
	p.tokenFieldMu.Lock()
	if p.tokenFields == nil {
		p.tokenFields = map[string]string{}
	}
	p.tokenFields[model] = field
	p.tokenFieldMu.Unlock()
}

// temperatureAllowed reports whether this model accepts a temperature.
func (p *Provider) temperatureAllowed(model string) bool {
	p.fixedTempMu.Lock()
	defer p.fixedTempMu.Unlock()
	return !p.fixedTemp[model]
}

// rememberFixedTemperature records that a model rejects non-default sampling,
// so the correction costs one round trip per model rather than one per call.
func (p *Provider) rememberFixedTemperature(model string) {
	p.fixedTempMu.Lock()
	if p.fixedTemp == nil {
		p.fixedTemp = map[string]bool{}
	}
	p.fixedTemp[model] = true
	p.fixedTempMu.Unlock()
}

// rejectsTemperature reports whether a rejection was about the temperature.
//
// Keyed on what the server said rather than on a model table, for the same
// reason as the token field: the set of models sampled at a fixed temperature
// changes, and a list Helix maintains is a list Helix gets wrong.
func rejectsTemperature(err error) bool {
	code, ok := providers.StatusCode(err)
	if !ok || code != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "temperature") &&
		(strings.Contains(msg, "unsupported") ||
			strings.Contains(msg, "does not support") ||
			strings.Contains(msg, "only the default"))
}

// correctedTokenField reads a rejection and reports which field to use instead.
//
// Driven by what the server actually said rather than by a model table: the
// error names both the parameter it refused and the one it wants, which is more
// reliable than any list Helix could keep current. Only a 400 is considered — a
// 401 or 500 says nothing about parameter names.
func correctedTokenField(err error, sent string) (string, bool) {
	code, ok := providers.StatusCode(err)
	if !ok || code != http.StatusBadRequest {
		return "", false
	}

	msg := err.Error()
	// The rejection must name the field we sent, or this is a different 400.
	if !strings.Contains(msg, sent) {
		return "", false
	}
	alt := providers.AlternateMaxTokensField(sent)
	if !strings.Contains(msg, alt) {
		return "", false
	}
	// max_completion_tokens contains "max_tokens" as a substring only in the
	// other direction, so guard against reading the suggestion as the refusal.
	if sent == providers.FieldMaxTokens && !strings.Contains(msg, "max_completion_tokens") {
		return "", false
	}
	return alt, true
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

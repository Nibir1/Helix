// internal/providers/types.go
// Purpose: Shared provider types for Helix Phase 1 AI engine.
package providers

import (
	"context"
)

// MessagePartType enumerates the kinds of content a message part can carry.
type MessagePartType string

const (
	// PartText is an additional text block.
	PartText MessagePartType = "text"

	// PartImageURL references an image by URL.
	PartImageURL MessagePartType = "image_url"

	// PartImage carries raw image bytes (base64-encoded at the adapter).
	PartImage MessagePartType = "image"
)

// MessagePart is one multimodal content block (BlackBox Phase 5). It is an
// in-memory extension: adapters translate Parts into their native wire format
// (OpenAI content arrays, Anthropic vision blocks, Ollama images). Text-only
// messages remain byte-identical on the wire.
type MessagePart struct {
	Type      MessagePartType `json:"type"`
	Text      string          `json:"text,omitempty"`
	ImageURL  string          `json:"image_url,omitempty"`
	ImageData []byte          `json:"image_data,omitempty"` // base64 at the adapter
}

// ChatMessage is a chat message. Content holds plain text; Parts optionally
// attaches multimodal blocks (Phase 5). Parts is excluded from the default
// JSON encoding so existing text-only adapters are unchanged.
type ChatMessage struct {
	Role    string        `json:"role"`
	Content string        `json:"content"`
	Parts   []MessagePart `json:"-"`
}

// HasImages reports whether the message carries image parts.
func (m ChatMessage) HasImages() bool {
	for _, p := range m.Parts {
		if p.Type == PartImage || p.Type == PartImageURL {
			return true
		}
	}
	return false
}

// ToolDefinition describes a function the model may call (BlackBox P8.7).
// Parameters is a JSON Schema object; adapters translate it into their native
// wire format.
type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall is a model's structured invocation of a tool. Arguments is the raw
// JSON string the model produced — deliberately not pre-parsed, so the caller
// validates it with its own schema rather than trusting a generic decode.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Tool-choice modes for ChatRequest.ToolChoice.
const (
	// ToolChoiceAuto lets the model decide whether to call a tool.
	ToolChoiceAuto = "auto"

	// ToolChoiceRequired forces a tool call. This is what makes native tool
	// calling worth using for the planner: the provider enforces the schema,
	// so the model cannot answer with prose instead of a plan.
	ToolChoiceRequired = "required"
)

// ChatRequest is the normalized request sent to any provider.
//
// Tools/ToolChoice carry no JSON tags (like ChatMessage.Parts): they are an
// in-memory extension that each adapter renders into its own wire format, so
// providers without tool support are unaffected.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stop        []string      `json:"stop,omitempty"`

	Tools      []ToolDefinition `json:"-"`
	ToolChoice string           `json:"-"`
}

// StreamChunk is one streamed token/event from a provider.
type StreamChunk struct {
	Content string
	Done    bool
	Error   error

	// ToolCalls carries fully assembled tool calls. Providers stream tool
	// arguments in fragments; adapters accumulate them and deliver complete
	// calls on the terminating chunk, so consumers never see partial JSON.
	ToolCalls []ToolCall
}

// ModelInfo describes one model exposed by a provider.
type ModelInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// Capabilities describes what a provider/model can do.
type Capabilities struct {
	Chat       bool `json:"chat"`
	Planner    bool `json:"planner"`
	Embeddings bool `json:"embeddings"`
	Vision     bool `json:"vision"`
	Local      bool `json:"local"`
	Remote     bool `json:"remote"`
	Streaming  bool `json:"streaming"`
	ToolUse    bool `json:"tool_use"`
}

// AIProvider is the unified provider contract.
type AIProvider interface {
	Name() string
	DisplayName() string
	Chat(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
	SetAPIKey(key string)
	ListModels(ctx context.Context) ([]ModelInfo, error)
	HealthCheck(ctx context.Context) error
	RequiresAPIKey() bool
	IsLocal() bool
	DefaultModel() string
	Capabilities() Capabilities
}

// EmbeddingProvider is implemented by providers that support embeddings.
type EmbeddingProvider interface {
	Embed(ctx context.Context, texts []string, model string) ([][]float32, error)
}

// ChatResult is the complete outcome of one chat call: assistant text and any
// tool calls the model made (BlackBox P8.7).
type ChatResult struct {
	Text      string
	ToolCalls []ToolCall
}

// CollectChat consumes a streaming chat response and returns the full text.
// Tool calls, if any, are discarded — callers that want them use
// CollectChatResult.
func CollectChat(ctx context.Context, p AIProvider, req ChatRequest) (string, error) {
	res, err := CollectChatResult(ctx, p, req)
	return res.Text, err
}

// CollectChatResult consumes a streaming chat response, returning both the
// assembled text and any tool calls.
//
// Partial text is returned alongside an error (unchanged from CollectChat):
// the planner's retry ladder inspects whatever arrived before a failure.
func CollectChatResult(ctx context.Context, p AIProvider, req ChatRequest) (ChatResult, error) {
	res := ChatResult{}

	ch, err := p.Chat(ctx, req)
	if err != nil {
		return res, err
	}

	for {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				return res, nil
			}

			if chunk.Error != nil {
				return res, chunk.Error
			}

			res.Text += chunk.Content
			if len(chunk.ToolCalls) > 0 {
				res.ToolCalls = append(res.ToolCalls, chunk.ToolCalls...)
			}

			if chunk.Done {
				return res, nil
			}
		}
	}
}

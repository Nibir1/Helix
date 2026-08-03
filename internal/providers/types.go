// internal/providers/types.go
// Purpose: Shared provider types for Helix Phase 1 AI engine.
package providers

import (
	"context"
)

// ChatMessage is a text-only chat message for Phase 1.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the normalized request sent to any provider.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
}

// StreamChunk is one streamed token/event from a provider.
type StreamChunk struct {
	Content string
	Done    bool
	Error   error
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

// CollectChat consumes a streaming chat response and returns the full text.
func CollectChat(ctx context.Context, p AIProvider, req ChatRequest) (string, error) {
	ch, err := p.Chat(ctx, req)
	if err != nil {
		return "", err
	}

	out := ""

	for {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				return out, nil
			}

			if chunk.Error != nil {
				return out, chunk.Error
			}

			out += chunk.Content

			if chunk.Done {
				return out, nil
			}
		}
	}
}

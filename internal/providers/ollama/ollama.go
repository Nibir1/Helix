// internal/providers/ollama/ollama.go
// Purpose: Ollama provider adapter.
package ollama

import (
	"context"
	"time"

	"helix/internal/ollama"
	"helix/internal/providers"
)

// Provider implements the Ollama provider.
type Provider struct {
	client *ollama.Client
}

// New creates an Ollama provider.
func New(client *ollama.Client) *Provider {
	return &Provider{client: client}
}

func (p *Provider) Name() string         { return "ollama" }
func (p *Provider) DisplayName() string  { return "Ollama" }
func (p *Provider) SetAPIKey(key string) {}

func (p *Provider) RequiresAPIKey() bool { return false }
func (p *Provider) IsLocal() bool        { return true }
func (p *Provider) DefaultModel() string { return "gemma4:e2b" }

func (p *Provider) Capabilities() providers.Capabilities {
	return providers.CapabilitiesFor("ollama", p.DefaultModel())
}

// Chat delegates to the native Ollama client.
func (p *Provider) Chat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamChunk, error) {
	return p.client.Chat(ctx, req)
}

// ListModels lists installed Ollama models.
func (p *Provider) ListModels(ctx context.Context) ([]providers.ModelInfo, error) {
	return p.client.ListModels(ctx)
}

// HealthCheck checks the Ollama daemon.
func (p *Provider) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	return p.client.Health(ctx)
}

// Embed implements providers.EmbeddingProvider using the native Ollama client,
// making local embeddings available without any API key.
//
// Args:
//   - ctx: cancellation/timeout context.
//   - texts: input strings.
//   - model: embedding model ("" → ollama.DefaultEmbeddingModel).
//
// Returns: one embedding per input text, or an error.
// Complexity: O(1) HTTP round trip on modern daemons.
func (p *Provider) Embed(ctx context.Context, texts []string, model string) ([][]float32, error) {
	return p.client.Embed(ctx, model, texts)
}

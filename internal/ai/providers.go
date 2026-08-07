// internal/ai/providers.go
// Purpose: Global provider registry and active provider state.
package ai

import (
	"context"
	"fmt"
	"time"

	"helix/internal/ollama"
	"helix/internal/providers"
	anthropicprovider "helix/internal/providers/anthropic"
	deepseekprovider "helix/internal/providers/deepseek"
	glmprovider "helix/internal/providers/glm"
	kimiprovider "helix/internal/providers/kimi"
	llamacppprovider "helix/internal/providers/llamacpp"
	ollamaprovider "helix/internal/providers/ollama"
	openaiprovider "helix/internal/providers/openai"
	openaicompatible "helix/internal/providers/openai_compatible"
	qwenprovider "helix/internal/providers/qwen"
)

// ProviderType is retained for backward compatibility.
type ProviderType string

const (
	ProviderOpenAI ProviderType = "openai"
	ProviderLocal  ProviderType = "llamacpp"
)

// ProviderSettings carries persisted provider settings into InitProviders.
type ProviderSettings struct {
	Provider      string
	Model         string
	CustomBaseURL string
}

var (
	registry       *providers.Registry
	activeProvider providers.AIProvider
	activeModel    string
	ollamaClient   = ollama.NewClient()
)

// InitProviders builds the provider registry.
func InitProviders(settings ProviderSettings) error {
	keys, err := providers.NewKeyStore()
	if err != nil {
		return fmt.Errorf("initialize keystore: %w", err)
	}

	client := providers.NewHTTPClient(60 * time.Second)
	registry = providers.NewRegistry(keys, client)

	registry.Register(openaiprovider.New(keys.Get("openai"), client))
	registry.Register(anthropicprovider.New(keys.Get("anthropic"), client))
	registry.Register(deepseekprovider.New(keys.Get("deepseek"), client))
	registry.Register(kimiprovider.New(keys.Get("kimi"), client))
	registry.Register(qwenprovider.New(keys.Get("qwen"), client))
	registry.Register(glmprovider.New(keys.Get("glm"), client))
	registry.Register(ollamaprovider.New(ollamaClient))
	registry.Register(llamacppprovider.New(client))

	if settings.CustomBaseURL != "" {
		if err := RegisterCustomProvider(settings.CustomBaseURL, keys.Get("custom")); err != nil {
			return err
		}
	}

	if settings.Provider != "" {
		if p, err := registry.Get(settings.Provider); err == nil {
			activeProvider = p
			activeModel = settings.Model

			if activeModel == "" {
				activeModel = p.DefaultModel()
			}
		}
	}

	return nil
}

// RegisterCustomProvider registers a custom OpenAI-compatible endpoint.
func RegisterCustomProvider(baseURL, apiKey string) error {
	if registry == nil {
		return fmt.Errorf("provider registry not initialized")
	}

	if baseURL == "" {
		return fmt.Errorf("custom provider base URL is empty")
	}

	registry.Register(openaicompatible.New(openaicompatible.Config{
		Name:         "custom",
		DisplayName:  "Custom OpenAI-Compatible",
		BaseURL:      baseURL,
		APIKey:       apiKey,
		DefaultModel: "custom",
		Local:        false,
	}, registry.Client()))

	return nil
}

// UseProvider sets the active provider.
func UseProvider(name string) error {
	if registry == nil {
		return fmt.Errorf("provider registry not initialized")
	}

	p, err := registry.Get(name)
	if err != nil {
		return err
	}

	activeProvider = p

	if activeModel == "" {
		activeModel = p.DefaultModel()
	}

	return nil
}

// UseModel sets the active model.
func UseModel(model string) {
	activeModel = model
}

// ActiveModel returns the active model.
func ActiveModel() string {
	return activeModel
}

// ActiveProviderName returns the active provider name.
func ActiveProviderName() string {
	if activeProvider == nil {
		return ""
	}

	return activeProvider.Name()
}

// HasProvider reports whether a provider is registered.
func HasProvider(name string) bool {
	if registry == nil {
		return false
	}

	_, err := registry.Get(name)
	return err == nil
}

// ListProviders returns registered provider names.
func ListProviders() []string {
	if registry == nil {
		return []string{}
	}

	return registry.Names()
}

// ListProviderModels lists models from the active provider.
func ListProviderModels(ctx context.Context) ([]providers.ModelInfo, error) {
	if activeProvider == nil {
		return nil, fmt.Errorf("no active provider")
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	return activeProvider.ListModels(ctx)
}

// DefaultModelForProvider returns the default model for a provider.
func DefaultModelForProvider(name string) string {
	if registry == nil {
		return ""
	}

	p, err := registry.Get(name)
	if err != nil {
		return ""
	}

	return p.DefaultModel()
}

// SaveProviderKey stores a provider API key.
func SaveProviderKey(provider, key string) error {
	if registry == nil {
		return fmt.Errorf("provider registry not initialized")
	}

	return registry.SetAPIKey(provider, key)
}

// ProviderHasSavedKey reports whether a provider key exists.
func ProviderHasSavedKey(provider string) bool {
	if registry == nil {
		return false
	}

	return registry.HasAPIKey(provider)
}

// ProviderStatus returns human-readable provider status lines.
func ProviderStatus() []string {
	if registry == nil {
		return []string{"provider registry not initialized"}
	}

	lines := make([]string, 0)

	for _, name := range registry.Names() {
		p, err := registry.Get(name)
		if err != nil {
			continue
		}

		keyStatus := "local/no key"

		if p.RequiresAPIKey() {
			if registry.HasAPIKey(name) {
				keyStatus = "API key configured"
			} else {
				keyStatus = "API key missing"
			}
		}

		active := ""
		if activeProvider != nil && activeProvider.Name() == name {
			active = " (active)"
		}

		lines = append(lines, fmt.Sprintf(
			"%s - %s - %s%s",
			name,
			p.DisplayName(),
			keyStatus,
			active,
		))
	}

	return lines
}

// GetProvider returns the active provider name for backward compatibility.
func GetProvider() ProviderType {
	if activeProvider == nil {
		return ProviderType("none")
	}

	return ProviderType(activeProvider.Name())
}

// SetProvider sets the active provider for backward compatibility.
func SetProvider(p ProviderType) {
	_ = UseProvider(string(p))
}

// ConfigureOpenAIKey is retained for backward compatibility.
func ConfigureOpenAIKey(key string) {
	if registry == nil {
		return
	}

	_ = registry.SetAPIKey("openai", key)
}

// HasOpenAIKey reports whether an OpenAI key is available.
func HasOpenAIKey() bool {
	if registry == nil {
		return false
	}

	return registry.HasAPIKey("openai")
}

// LoadOpenAIKeyFromDisk is retained for backward compatibility.
func LoadOpenAIKeyFromDisk() (string, error) {
	if registry == nil {
		return "", fmt.Errorf("provider registry not initialized")
	}

	return registry.Keys().Get("openai"), nil
}

// SaveOpenAIKeyToDisk is retained for backward compatibility.
func SaveOpenAIKeyToDisk(key string) error {
	if registry == nil {
		return fmt.Errorf("provider registry not initialized")
	}

	return registry.SetAPIKey("openai", key)
}

// GetEmbeddings produces embeddings with the best available backend:
// OpenAI when a key exists, then the active provider, then local Ollama.
//
// Args:
//   - texts: input strings.
//
// Returns: one embedding per input text, or an error when no backend exists.
// Complexity: O(1) HTTP round trip(s) depending on backend.
func GetEmbeddings(texts []string) ([][]float32, error) {
	if registry == nil {
		return nil, fmt.Errorf("provider registry not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1) OpenAI when a key exists (highest-quality embeddings).
	if registry.HasAPIKey("openai") {
		if p, err := registry.Get("openai"); err == nil {
			if ep, ok := p.(providers.EmbeddingProvider); ok {
				return ep.Embed(ctx, texts, "")
			}
		}
	}

	// 2) Active provider when it supports embeddings (e.g. Ollama).
	if activeProvider != nil {
		if ep, ok := activeProvider.(providers.EmbeddingProvider); ok {
			return ep.Embed(ctx, texts, "")
		}
	}

	// 3) Local Ollama fallback (no OpenAI key required).
	if p, err := registry.Get("ollama"); err == nil {
		if ep, ok := p.(providers.EmbeddingProvider); ok {
			return ep.Embed(ctx, texts, "")
		}
	}

	return nil, fmt.Errorf("embeddings are not supported by any available provider")
}

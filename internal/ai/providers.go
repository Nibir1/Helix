// internal/ai/providers.go
// Purpose: Global provider registry and active provider state.
// Hardening: GetEmbeddings registers its cancel func with the interrupt
// manager so Ctrl+C aborts embedding HTTP calls instead of killing Helix.
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
	xaiprovider "helix/internal/providers/xai"
	"helix/internal/utils"
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

	// LlamaCppBaseURL points at a user-managed llama-server (P11.4). Empty
	// falls back to HELIX_LLAMACPP_URL, then llama-server's default port.
	LlamaCppBaseURL string
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
	registry.Register(xaiprovider.New(keys.Get("xai"), client))
	registry.Register(ollamaprovider.New(ollamaClient))
	// llama.cpp is always registered (P11.4): it needs no key and costs nothing
	// until used, and pre-registering it makes it selectable as the Phase 11
	// local fallback on boards where Ollama is unsupported.
	registry.Register(llamacppprovider.New(settings.LlamaCppBaseURL, client))
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

// GetProviderByName returns a registered provider without switching the active
// one (BlackBox Phase 5 dedicated vision-provider routing).
func GetProviderByName(name string) (providers.AIProvider, error) {
	if registry == nil {
		return nil, fmt.Errorf("provider registry not initialized")
	}
	return registry.Get(name)
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
	// A deliberate choice outranks the P11.2 breaker: without this, a later
	// automatic restore would silently revert what the user just selected.
	clearDegradedForUserOverride()
	return nil
}

// UseModel sets the active model.
func UseModel(model string) {
	activeModel = model
	clearDegradedForUserOverride()
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

// ProviderKey returns the stored key for a provider, or "". See
// Registry.APIKey for why this is reachable at all.
func ProviderKey(provider string) string {
	if registry == nil {
		return ""
	}
	return registry.APIKey(provider)
}

// ProviderStatus returns human-readable provider status lines.
// ProviderRow is one provider's state, structured.
//
// ProviderStatus below returns pre-formatted strings, which meant every caller
// that wanted to RENDER the state (badges, a table, a colour per key state) had
// to parse " - " back apart — the same re-parse-your-own-output mistake the
// metrics reader was created to avoid. New callers use this.
type ProviderRow struct {
	Name     string
	Display  string
	KeyState string // "configured", "missing", or "" for a keyless local provider
	Local    bool
	Active   bool
}

// ProviderStatusRows reports every registered provider's state.
func ProviderStatusRows() []ProviderRow {
	if registry == nil {
		return nil
	}
	out := make([]ProviderRow, 0)
	for _, name := range registry.Names() {
		p, err := registry.Get(name)
		if err != nil {
			continue
		}
		row := ProviderRow{Name: name, Display: p.DisplayName(), Local: !p.RequiresAPIKey()}
		if p.RequiresAPIKey() {
			row.KeyState = "missing"
			if registry.HasAPIKey(name) {
				row.KeyState = "configured"
			}
		}
		row.Active = activeProvider != nil && activeProvider.Name() == name
		out = append(out, row)
	}
	return out
}

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
	// FIX (interrupt hardening): Ctrl+C while waiting on embedding HTTP calls
	// cancels the request and returns control to the prompt.
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
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

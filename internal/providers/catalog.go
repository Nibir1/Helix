// internal/providers/catalog.go
// Purpose: Model context limits and capability flags.
package providers

import (
	"strings"
)

// DefaultContextLimit is the conservative fallback for unknown models.
const DefaultContextLimit = 8_192

type contextLimitEntry struct {
	prefix string
	limit  int
}

var contextLimits = []contextLimitEntry{
	// OpenAI
	{prefix: "gpt-5.6-luna", limit: 1_050_000},
	{prefix: "gpt-5.6-sol", limit: 1_050_000}, // Added for standard API alias
	{prefix: "gpt-5.5", limit: 1_000_000},
	{prefix: "gpt-5.4-pro", limit: 1_050_000},
	{prefix: "gpt-5.4-mini", limit: 400_000},
	{prefix: "gpt-5.4-nano", limit: 272_000},
	{prefix: "gpt-4o", limit: 128_000},

	// Anthropic
	{prefix: "claude-fable-5", limit: 1_000_000},
	{prefix: "claude-opus-5", limit: 1_000_000},
	{prefix: "claude-opus-4-8", limit: 1_000_000},
	{prefix: "claude-opus-4-6", limit: 1_000_000},
	{prefix: "claude-sonnet-5", limit: 1_000_000},

	// DeepSeek
	{prefix: "deepseek-v4-pro", limit: 1_000_000},
	{prefix: "deepseek-v4-flash", limit: 1_000_000},
	{prefix: "deepseek-chat", limit: 1_000_000},     // Public API alias for V3/V4-Flash
	{prefix: "deepseek-reasoner", limit: 1_000_000}, // Public API alias for R1/V4-Pro

	// GLM
	{prefix: "glm-5.2", limit: 1_000_000},
	{prefix: "glm-5.1", limit: 200_000},

	// Kimi
	{prefix: "kimi-k3", limit: 1_000_000},
	{prefix: "kimi-k2.6", limit: 1_000_000},

	// Qwen
	{prefix: "qwen3.7-plus", limit: 1_000_000},

	// Local common models
	{prefix: "phi4-mini", limit: 128_000},
	{prefix: "phi4", limit: 128_000},
	{prefix: "phi3", limit: 128_000},
	{prefix: "llama3.3", limit: 128_000},
	{prefix: "llama3.2", limit: 128_000},
	{prefix: "llama3.1", limit: 128_000},
	{prefix: "llama3", limit: 8_192},
	{prefix: "gemma3", limit: 128_000},
	{prefix: "gemma2", limit: 8_192},
	{prefix: "mistral-nemo", limit: 128_000},
	{prefix: "mistral-small", limit: 128_000},
	{prefix: "mistral-large", limit: 128_000},
	{prefix: "mistral", limit: 32_000},
	{prefix: "qwen3:4b", limit: 128_000},
	{prefix: "qwen2.5", limit: 128_000},
	{prefix: "tinyllama", limit: 2_048},
}

// GetContextLimit returns the context window size in tokens.
func GetContextLimit(modelID string) int {
	modelID = strings.ToLower(strings.TrimSpace(modelID))

	if modelID == "" {
		return DefaultContextLimit
	}

	for _, entry := range contextLimits {
		if strings.HasPrefix(modelID, entry.prefix) {
			return entry.limit
		}
	}

	return DefaultContextLimit
}

// GetSafeContentLimit returns a conservative character budget for prompts.
func GetSafeContentLimit(modelID string) int {
	contextLimit := GetContextLimit(modelID)
	reservedTokens := 8_000

	availableTokens := contextLimit - reservedTokens
	if availableTokens < 1_000 {
		availableTokens = 1_000
	}

	return availableTokens * 4
}

// CapabilitiesFor returns capability flags for a provider/model pair.
func CapabilitiesFor(provider, model string) Capabilities {
	model = strings.ToLower(model)

	local := provider == "ollama" || provider == "llamacpp"

	return Capabilities{
		Chat:       true,
		Planner:    GetContextLimit(model) >= 8_192,
		Embeddings: provider == "openai",
		Vision:     strings.Contains(model, "vision") || strings.Contains(model, "gemma3"),
		Local:      local,
		Remote:     !local,
		Streaming:  true,
		ToolUse:    false,
	}
}

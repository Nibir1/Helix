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

	// xAI (Grok) — context windows from docs.x.ai/docs/models. Without these
	// the default 8k applies and GetSafeContentLimit clamps RAG context to a
	// fraction of what Grok can actually take.
	{prefix: "grok-4.6", limit: 500_000},
	{prefix: "grok-4.5", limit: 500_000},
	{prefix: "grok-4.3", limit: 1_000_000},
	{prefix: "grok-4.20", limit: 1_000_000},
	{prefix: "grok-build", limit: 256_000},

	// Gemma
	{prefix: "gemma4", limit: 128_000},
	{prefix: "gemma-4", limit: 128_000},
	{prefix: "gemma3", limit: 128_000},
	{prefix: "gemma2", limit: 8_192},

	// Local common models
	{prefix: "phi4-mini", limit: 128_000},
	{prefix: "phi4", limit: 128_000},
	{prefix: "phi3", limit: 128_000},
	{prefix: "llama3.3", limit: 128_000},
	{prefix: "llama3.2", limit: 128_000},
	{prefix: "llama3.1", limit: 128_000},
	{prefix: "llama3", limit: 8_192},
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

// toolUseProviders are the providers whose Helix adapter actually implements
// native function calling AND whose current chat models support it.
//
// This deliberately describes the ADAPTER's ability, not just the vendor's:
// the flag is consumed as "can Helix use tool calling here", so listing a
// provider Helix cannot yet drive (Anthropic, Ollama — both have their own
// non-OpenAI wire formats) would cost a wasted round trip on every planner
// call before the fallback kicked in.
//
// "custom" is excluded on purpose: an arbitrary OpenAI-compatible endpoint may
// or may not implement /tools, and guessing wrong is worse than not trying.
// llama.cpp is excluded for the same reason — llama-server's tool support
// depends on the loaded GGUF and a --jinja launch flag Helix cannot detect.
var toolUseProviders = map[string]bool{
	"openai":    true,
	"deepseek":  true,
	"kimi":      true,
	"qwen":      true,
	"glm":       true,
	"xai":       true, // docs.x.ai lists function calling
	"anthropic": true, // P8.7b: tool_use blocks + input_schema
	"ollama":    true, // P8.7b: /api/chat tools — but MODEL-gated, see below
}

// ollamaToolModels are the local model families whose Ollama builds ship a
// tool-calling template.
//
// This gate is not pedantry. Helix's own default local model is `gemma4:e2b`,
// and Gemma has NO tool template — advertising tool support for every Ollama
// model would make the planner attempt a tool call, get prose back, and fall
// through to the prompt ladder on EVERY plan, burning a wasted round trip on
// exactly the low-powered hardware that can least afford one.
var ollamaToolModels = []string{
	"llama3.1", "llama3.2", "llama3.3",
	"qwen2.5", "qwen3",
	"mistral-nemo", "mistral-small", "mistral-large",
	"command-r", "firefunction", "hermes3",
}

// SupportsToolUse reports whether native function calling can be used for a
// provider/model pair (BlackBox P8.7/P8.7b).
func SupportsToolUse(provider, model string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	if !toolUseProviders[p] {
		return false
	}
	m := strings.ToLower(strings.TrimSpace(model))

	// Embedding endpoints share the provider name but have no tool support.
	if strings.Contains(m, "embedding") {
		return false
	}

	// Cloud providers ship tool support across their current chat models;
	// Ollama's depends entirely on the individual model's template.
	if p != "ollama" {
		return true
	}
	for _, family := range ollamaToolModels {
		if strings.HasPrefix(m, family) {
			return true
		}
	}
	return false
}

// visionModelSubstrings are model-name fragments that mark a multimodal model
// regardless of provider. Substring, not prefix: vendors bury the marker
// mid-name ("qwen2.5-vl-7b", "llama-3.2-11b-vision-instruct").
var visionModelSubstrings = []string{
	// Explicitly-named multimodal builds.
	"vision", "llava", "-vl", "vl-", "moondream", "minicpm-v", "bakllava",
	// Families that are multimodal across the board.
	"gpt-4o", "gpt-4.1", "gpt-5", "o3", "o4-mini",
	"claude-3", "claude-4", "claude-sonnet", "claude-opus", "claude-haiku",
	"gemini", "gemma3", "gemma4",
	"pixtral", "grok-2-vision", "grok-4",
	"qwen2.5-vl", "qwen3-vl", "glm-4v", "glm-4.5v",
	"internvl", "phi-3.5-vision", "phi-4-multimodal",
}

// SupportsVision reports whether a provider/model pair can process images.
//
// It replaces a three-substring test (`vision`, `gemma3`, `llava`) that missed
// essentially every mainstream multimodal model — gpt-4o, every Claude 3/4, all
// Gemini, and Ollama's own shipped default `gemma4:e2b`. The consequence was not
// subtle: /eyes on refused with "No vision-capable model is configured" on
// providers that see perfectly well, so the whole Phase 5 camera path was
// unreachable on a normal cloud setup.
//
// Matching is on the MODEL name, deliberately: vision is a per-model property,
// and a provider-level allowlist would claim vision for a text-only model from a
// vendor that also ships multimodal ones.
//
// Args:
//   - provider: registry provider name (currently advisory; kept for symmetry
//     with SupportsToolUse and for future provider-level carve-outs).
//   - model: the model that will actually be called.
//
// Returns: true when images can be sent.
// Complexity: O(len(visionModelSubstrings)).
func SupportsVision(provider, model string) bool {
	_ = provider
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	// Embedding endpoints share provider names but take no images.
	if strings.Contains(m, "embedding") {
		return false
	}
	for _, frag := range visionModelSubstrings {
		if strings.Contains(m, frag) {
			return true
		}
	}
	return false
}

// CapabilitiesFor returns capability flags for a provider/model pair.
func CapabilitiesFor(provider, model string) Capabilities {
	model = strings.ToLower(model)

	local := provider == "ollama" || provider == "llamacpp"

	return Capabilities{
		Chat:       true,
		Planner:    GetContextLimit(model) >= 8_192,
		Embeddings: provider == "openai" || provider == "ollama",
		Vision:     SupportsVision(provider, model),
		Local:      local,
		Remote:     !local,
		Streaming:  true,
		ToolUse:    SupportsToolUse(provider, model),
	}
}

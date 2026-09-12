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
	{prefix: "gpt-5.6-terra", limit: 1_050_000},
	{prefix: "gpt-5.6", limit: 1_050_000}, // bare alias for -sol
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
	{prefix: "glm-5.3-flash", limit: 1_048_576},
	{prefix: "glm-5.3", limit: 1_000_000},
	{prefix: "glm-5.2", limit: 1_000_000},
	{prefix: "glm-5.1", limit: 200_000},
	{prefix: "glm-4.6v", limit: 128_000},

	// Google Gemini — context windows from ai.google.dev. Without these the 8k
	// default applies and GetSafeContentLimit clamps RAG context to ~4k chars,
	// which is the silent-starvation bug the xAI entry below was added for.
	{prefix: "gemini-3.7", limit: 1_000_000},
	{prefix: "gemini-3.6", limit: 1_000_000},
	{prefix: "gemini-3.5", limit: 1_000_000},
	{prefix: "gemini-3.1-pro", limit: 1_000_000},
	{prefix: "gemini-3", limit: 1_000_000},
	{prefix: "gemini-2.5", limit: 1_000_000},

	// Meta (Meta Model API)
	{prefix: "muse-spark", limit: 1_048_576},

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
	"gemini":    true, // OpenAI-compatible surface forwards tools/tool_choice
	"meta":      true, // Muse Spark lists tool calling
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

// MaxTokensField is the request field that bounds a completion.
const (
	// FieldMaxTokens is the original OpenAI parameter, and what every
	// OpenAI-COMPATIBLE server understands (llama.cpp, Ollama, Groq, DeepSeek,
	// xAI, GLM, Kimi, Qwen). It stays the default for that reason.
	FieldMaxTokens = "max_tokens"

	// FieldMaxCompletionTokens is OpenAI's replacement. Reasoning models
	// (GPT-5.x, o1, o3, o4) REJECT max_tokens outright with an
	// unsupported_parameter 400, because that bound could not account for the
	// internal reasoning tokens they generate.
	FieldMaxCompletionTokens = "max_completion_tokens"
)

// maxCompletionTokensModels are the model-name prefixes that require
// FieldMaxCompletionTokens.
//
// Prefix matching on the MODEL, not the provider: an OpenAI-compatible proxy
// ("custom") may well be serving gpt-5, and OpenAI itself still serves older
// models that only accept max_tokens. Note that gpt-4-turbo accepts ONLY
// max_tokens, so this cannot be widened to "everything OpenAI".
var maxCompletionTokensModels = []string{
	"gpt-5", "o1", "o1-mini", "o1-preview", "o3", "o3-mini", "o4", "o4-mini",
}

// PreferredMaxTokensField returns the completion-bound field to send first.
//
// It is a starting guess, not a verdict: the adapter recovers from a wrong guess
// by reading the server's own correction and retrying once, then remembering the
// answer. That matters because this list ages — a model released tomorrow will
// not be in it, and a hardcoded table alone would fail closed on exactly the
// newest models.
func PreferredMaxTokensField(provider, model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range maxCompletionTokensModels {
		if m == prefix || strings.HasPrefix(m, prefix+"-") || strings.HasPrefix(m, prefix+".") {
			return FieldMaxCompletionTokens
		}
	}
	return FieldMaxTokens
}

// AlternateMaxTokensField returns the other field, for the retry.
func AlternateMaxTokensField(field string) string {
	if field == FieldMaxCompletionTokens {
		return FieldMaxTokens
	}
	return FieldMaxCompletionTokens
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
	"qwen2.5-vl", "qwen3-vl", "qwen3.7-plus", "glm-4v", "glm-4.5v", "glm-4.6v",
	"internvl", "phi-3.5-vision", "phi-4-multimodal",
	// Natively multimodal flagships whose names carry no "vision"/"vl" marker
	// at all. Each is somebody's DEFAULT model, so missing one here is the
	// difference between /eyes working and refusing on a stock install.
	//
	// The DeepSeek entries were MEASURED, by sending a four-quadrant JPEG
	// through Helix's own provider path and asking for the colours. Reported
	// from a real session where /blackbox eyes on refused on a model that sees.
	//
	//	deepseek-flash      4/4   the current default
	//	deepseek-chat       4/4   alias
	//	deepseek-reasoner   4/4   alias
	//	deepseek-coder      4/4   alias
	//	deepseek-v4-pro     0/4   "I can't see the image" — and it is NOT here
	//
	// /v1/models serves only `deepseek-flash` and `deepseek-v4-pro`; the other
	// three names are aliases the API resolves onto the flagship, which is why
	// they see. That is also why this is NOT a provider-level rule: v4-pro is a
	// real, listed, text-only model, so "deepseek sees" would send it an image.
	//
	// Guessing got this wrong in BOTH directions. The retired
	// `deepseek-v4-flash-vision-exp` carried the literal word "vision" and
	// matched by accident, so the table looked right until the vendor shipped a
	// successor with a cleaner name; and the first fix shipped a test asserting
	// the three aliases were text-only, which the measurement disproved.
	"glm-5.3-flash", "muse-spark",
	"deepseek-flash", "deepseek-chat", "deepseek-reasoner", "deepseek-coder",
	"kimi-k3", "kimi-k2.6", "kimi-k2.5",
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

// visionCapableVendors are vendors whose current line-up includes a
// multimodal model.
//
// The provider-level floor for when no model is selected yet. It exists
// because Capabilities() used to answer by inspecting a compiled-in default
// model ID — so removing those IDs would have made every provider report that
// it cannot see until the user picked something, and /blackbox eyes would
// refuse on a fresh key with no explanation.
//
// "Anthropic ships multimodal models" ages far better than "claude-opus-5
// exists", which is the whole reason this is a vendor list and not a model
// list. Consulted only when the model cache knows nothing.
var visionCapableVendors = map[string]bool{
	"openai": true, "anthropic": true, "gemini": true, "deepseek": true,
	"glm": true, "kimi": true, "meta": true, "qwen": true, "xai": true,
	"ollama": true,
}

// isLocalProvider reports whether a provider runs on this machine. Extracted
// from an inline expression that two capability functions now need, so the two
// cannot come to disagree about what "local" means.
func isLocalProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ollama", "llamacpp":
		return true
	default:
		return false
	}
}

// VendorLikelySees reports whether a vendor is known to serve a model that can
// process images, without naming one.
func VendorLikelySees(provider string) bool {
	return visionCapableVendors[strings.ToLower(strings.TrimSpace(provider))]
}

// CapabilitiesForProvider answers what a provider can do when no model has
// been selected.
//
// Everything except Vision is already a provider-level fact in this file;
// Vision is the one that used to need a model name, and the caller supplies
// what it knows about the provider's live catalogue (see
// ModelCache.AnyVisionModel) so a real answer beats the vendor list whenever
// one is available.
func CapabilitiesForProvider(provider string, knownVisionModel bool) Capabilities {
	name := strings.ToLower(strings.TrimSpace(provider))
	return Capabilities{
		Chat:       true,
		Streaming:  true,
		Planner:    true,
		Embeddings: name == "openai" || name == "ollama",
		Vision:     knownVisionModel || VendorLikelySees(name),
		Local:      isLocalProvider(name),
		Remote:     !isLocalProvider(name),
		ToolUse:    toolUseProviders[name],
	}
}

// CapabilitiesFor returns capability flags for a provider/model pair.
func CapabilitiesFor(provider, model string) Capabilities {
	model = strings.ToLower(model)

	local := isLocalProvider(provider)

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

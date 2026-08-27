// internal/providers/vision_capability_test.go
// Purpose: SupportsVision must recognize the models people actually run. The
// previous test — three substrings, `vision`/`gemma3`/`llava` — reported
// Vision:false for gpt-4o, every Claude, all Gemini, and Helix's own shipped
// Ollama default, which made /eyes on unreachable on a normal setup.
package providers

import "testing"

func TestSupportsVisionRecognizesMainstreamMultimodalModels(t *testing.T) {
	capable := map[string]string{
		"openai":    "gpt-4o",
		"openai-5":  "gpt-5",
		"openai-41": "gpt-4.1-mini",
		"anthropic": "claude-sonnet-4-5",
		"claude-3":  "claude-3-5-haiku-20241022",
		"gemini":    "gemini-2.0-flash",
		// Helix's own shipped Ollama default, which the old `gemma3` substring
		// missed by one digit.
		"ollama-default": "gemma4:e2b",
		"ollama-llava":   "llava:13b",
		"ollama-gemma3":  "gemma3:4b",
		"qwen-vl":        "qwen2.5-vl-7b-instruct",
		"llama-vision":   "llama-3.2-11b-vision-instruct",
		"pixtral":        "pixtral-12b",
		"glm-v":          "glm-4v-plus",
		"moondream":      "moondream:latest",
		// Natively multimodal flagships whose names carry no marker at all.
		// Each of these is some provider's DEFAULT, so a miss here is /eyes
		// refusing on a stock install.
		"gemini-default": "gemini-3.7-flash",
		"meta-default":   "muse-spark-1.2",
		"glm-default":    "glm-5.3-flash",
		"glm-4.6v":       "glm-4.6v",
		"kimi-default":   "kimi-k3",
		"kimi-k2.6":      "kimi-k2.6",
		"qwen-default":   "qwen3.7-plus",
		"openai-56":      "gpt-5.6-luna",
		"deepseek-vl":    "deepseek-v4-flash-vision-exp",
	}
	for label, model := range capable {
		t.Run(label, func(t *testing.T) {
			if !SupportsVision("", model) {
				t.Errorf("SupportsVision(%q) = false; this model can see", model)
			}
		})
	}
}

func TestSupportsVisionRejectsTextOnlyModels(t *testing.T) {
	textOnly := map[string]string{
		// The screenshot's active model: a bare local GGUF placeholder.
		"local placeholder": "local-gguf",
		"qwen text":         "qwen2.5:3b",
		"llama text":        "llama3.1:8b",
		"mistral":           "mistral-nemo",
		"deepseek":          "deepseek-chat",
		"empty":             "",
		// Embedding endpoints share provider names but take no images.
		"embedding": "text-embedding-3-large",
		// Same vendor, same generation, one letter of difference — these are
		// the near misses the substring list must NOT swallow. glm-5.3 is text
		// only while glm-5.3-flash sees; qwen3.7-max is text only while
		// qwen3.7-plus sees; kimi-k2.7-code is text only while k2.6 and k3 see.
		"glm-5.3 text":   "glm-5.3",
		"qwen3.7-max":    "qwen3.7-max",
		"kimi k2.7 code": "kimi-k2.7-code",
	}
	for label, model := range textOnly {
		t.Run(label, func(t *testing.T) {
			if SupportsVision("", model) {
				t.Errorf("SupportsVision(%q) = true; this model cannot see", model)
			}
		})
	}
}

// CapabilitiesFor must route through SupportsVision rather than keeping its own
// copy of the rule — two lists would drift.
func TestCapabilitiesForUsesSupportsVision(t *testing.T) {
	if !CapabilitiesFor("openai", "gpt-4o").Vision {
		t.Error("CapabilitiesFor should report vision for gpt-4o")
	}
	if CapabilitiesFor("llamacpp", "local-gguf").Vision {
		t.Error("CapabilitiesFor should not report vision for a text-only local model")
	}
}

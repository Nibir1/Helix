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
		// `deepseek-chat` WAS here, asserted text-only. It is not: measured 4/4
		// on a real image through Helix's own provider path, because DeepSeek
		// resolves the name onto its multimodal flagship. The row is replaced
		// rather than removed — deepseek-v4-pro is the vendor's genuine
		// text-only model (0/4, "I can't see the image"), so the case this row
		// was written to cover still has a subject.
		"deepseek text-only": "deepseek-v4-pro",
		"empty":              "",
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

// A vendor's CURRENT default must be recognised, not just the one that happened
// to carry "vision" in its name.
//
// Reported from a real session: `/blackbox eyes on` refused with "deepseek /
// deepseek-flash cannot process images" on a model DeepSeek documents as taking
// images (https://api-docs.deepseek.com/guides/vision), through the same
// OpenAI-compatible `image_url` content part openai_compatible.go already
// sends. Its predecessor `deepseek-v4-flash-vision-exp` matched by accident on
// the literal word, so this table looked correct right up until the vendor
// shipped a successor with a cleaner name — the failure mode the "natively
// multimodal flagships" comment was added to prevent, happening again.
//
// MEASURED against the live API, not read: a four-quadrant JPEG sent through
// Helix's own provider path, asking for the colour in each corner. deepseek-flash
// answered 4/4.
func TestCurrentVendorDefaultsAreRecognizedAsMultimodal(t *testing.T) {
	seeing := map[string]string{
		"deepseek":  "deepseek-flash",
		"glm":       "glm-5.3-flash",
		"kimi":      "kimi-k3",
		"meta":      "muse-spark",
		"anthropic": "claude-opus-5",
		"gemini":    "gemini-3.7-flash",
	}
	for provider, model := range seeing {
		if !SupportsVision(provider, model) {
			t.Errorf("SupportsVision(%q, %q) = false; /blackbox eyes on refuses on a stock "+
				"install of this vendor", provider, model)
		}
	}
}

// The vendor's ALIASES see, because they resolve onto the multimodal flagship.
//
// This test previously asserted the opposite — that deepseek-chat/reasoner/coder
// were text-only — which was a guess, and wrong. Measured: all three answer 4/4
// on the quadrant image. /v1/models serves only `deepseek-flash` and
// `deepseek-v4-pro`, so the other names are aliases the API resolves.
func TestAVendorsAliasesInheritTheFlagshipsVision(t *testing.T) {
	for _, model := range []string{"deepseek-chat", "deepseek-reasoner", "deepseek-coder"} {
		if !SupportsVision("deepseek", model) {
			t.Errorf("SupportsVision(%q) = false; measured 4/4 on a real image, so "+
				"/blackbox eyes on refuses for no reason", model)
		}
	}
}

// And the one that genuinely cannot. This is why vision stays a per-MODEL
// property: a provider-level "deepseek sees" rule would send v4-pro an image.
func TestARealTextOnlyModelFromAVisionVendorStaysTextOnly(t *testing.T) {
	if SupportsVision("deepseek", "deepseek-v4-pro") {
		t.Error("SupportsVision(\"deepseek-v4-pro\") = true; measured 0/4 — it replies " +
			"\"I can't see the image\", so Helix would promise a camera that cannot work")
	}
}

// The gate that actually refused. An empty capable-provider list is what
// produced "None of the registered providers offers a vision-capable default
// model." on a machine whose only provider sees perfectly well.
func TestAVisionVendorsDefaultMakesTheProviderCapable(t *testing.T) {
	if !VendorLikelySees("deepseek") {
		t.Error("deepseek is not in the vendor floor, so a fresh key refuses the camera")
	}
	// knownVisionModel=false is the case that matters: it is what the caller
	// passes when the model cache has not confirmed one, so the answer falls
	// through to the vendor floor.
	if caps := CapabilitiesForProvider("deepseek", false); !caps.Vision {
		t.Error("CapabilitiesForProvider(\"deepseek\", false).Vision is false")
	}
}

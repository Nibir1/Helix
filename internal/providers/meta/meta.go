// internal/providers/meta/meta.go
//
// Purpose: Meta's hosted models over the Meta Model API.
//
// Naming, because it moved: Meta's developer API was `api.llama.com` serving
// `Llama-*` models. It is now the **Meta Model API** at `https://api.meta.ai/v1`
// serving the **Muse Spark** family, and it is OpenAI-SDK compatible — so this
// is the same thin configuration of the shared adapter as xAI and Gemini.
//
// The registry key is `meta`, the company, not `llama`, the old model family:
// Helix also runs Llama weights locally through Ollama and llama.cpp, and a
// provider called "llama" that meant "Meta's cloud" would collide with both.
package meta

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

const (
	// Name is the registry key.
	Name = "meta"

	// DisplayName names the company and the model family, so the picker shows
	// both words a user might be looking for.
	DisplayName = "Meta (Muse Spark)"

	// BaseURL is the Meta Model API's OpenAI-compatible endpoint.
	BaseURL = "https://api.meta.ai/v1"

	// DefaultModel is the current Standard-tier checkpoint: 1,048,576-token
	// context, tool calling, and multimodal input (text, image, video, audio,
	// PDF) — chosen for the vision, so the camera path works on a fresh key.
	DefaultModel = "muse-spark-1.2"
)

// New creates the Meta provider.
//
// Args:
//   - apiKey: a Meta Model API key (META_API_KEY, or Meta's own documented
//     MODEL_API_KEY — the keystore accepts either).
//   - client: the shared retrying HTTP client.
//
// Returns: a registry-ready provider.
// Complexity: O(1).
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         Name,
		DisplayName:  DisplayName,
		BaseURL:      BaseURL,
		APIKey:       apiKey,
		DefaultModel: DefaultModel,
	}, client)
}

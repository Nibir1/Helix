// internal/providers/xai/xai.go
//
// Purpose: xAI (Grok) provider over the OpenAI-compatible Messages API.
//
// Naming, because it causes real mistakes: **xAI** is the company, **Grok** is
// its model family, and `https://api.x.ai/v1` is the endpoint — one account,
// one key. That is entirely separate from **Groq** (GroqCloud,
// `https://api.groq.com/openai/v1`), which Helix uses for cheap Whisper STT
// (ADR-011). The names differ by one letter and the keys are not
// interchangeable; providers.MisdirectedKey guards the paste.
//
// The API is OpenAI-compatible and supports function calling, so this is a thin
// configuration of the shared adapter and Grok participates in native planner
// tool calling (P8.7) with no extra wiring.
package xai

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

const (
	// Name is the registry key.
	Name = "xai"

	// DisplayName names both company and model family, so `/provider status`
	// makes the Groq/Grok distinction visible at the point of choice.
	DisplayName = "xAI (Grok)"

	// BaseURL is xAI's OpenAI-compatible endpoint (docs.x.ai).
	BaseURL = "https://api.x.ai/v1"

	// DefaultModel is used until the user picks one; the wizard lists the live
	// catalogue from /models.
	DefaultModel = "grok-4.6"
)

// New creates the xAI provider.
//
// Args:
//   - apiKey: an xAI key from console.x.ai (NOT a Groq key).
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

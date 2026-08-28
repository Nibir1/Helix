// internal/providers/gemini/gemini.go
//
// Purpose: Google Gemini over its OpenAI-compatible endpoint.
//
// Google publishes an OpenAI-compatible surface at
// `https://generativelanguage.googleapis.com/v1beta/openai/` — Bearer auth,
// `/chat/completions`, `/models`, and the same `image_url` content parts the
// shared adapter already emits. So this is a thin configuration rather than a
// second wire format, and Gemini gets streaming, native tool calling (P8.7) and
// the Phase 5 camera path with no extra code.
//
// The trailing slash in Google's documented base URL is deliberate on their
// side and harmless here: the adapter trims it before appending a path.
package gemini

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

const (
	// Name is the registry key.
	Name = "gemini"

	// DisplayName names the vendor as well as the family, because "Gemini" and
	// "Gemma" are one letter apart and Helix serves Gemma locally via Ollama.
	DisplayName = "Google Gemini"

	// BaseURL is Gemini's OpenAI-compatible endpoint (ai.google.dev).
	BaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"

	// DefaultModel is Google's newest stable general-purpose model: 1M context,
	// text+image+audio+video input, function calling. Vision is not incidental
	// here — a default that cannot see makes /eyes unreachable on a fresh
	// install, which is the whole point of choosing it.
	DefaultModel = "gemini-3.7-flash"
)

// New creates the Gemini provider.
//
// Args:
//   - apiKey: a Gemini API key from aistudio.google.com (AIza…).
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

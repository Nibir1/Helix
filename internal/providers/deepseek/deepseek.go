// internal/providers/deepseek/deepseek.go
// Purpose: DeepSeek provider wrapper.
package deepseek

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// DefaultModel is DeepSeek's multimodal build.
//
// It is the ONLY DeepSeek model that accepts images — every other one returns a
// 400 for an image part — and it matches deepseek-v4-flash on text, agents and
// reasoning, so choosing it for the vision costs nothing on ordinary chat. The
// `-exp` suffix is DeepSeek's, not a Helix opinion: it is the name they ship the
// multimodal API under, and `/model use deepseek-v4-flash` remains one command
// away for anyone who wants the text-only build.
const DefaultModel = "deepseek-v4-flash-vision-exp"

// New creates a DeepSeek provider.
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "deepseek",
		DisplayName:  "DeepSeek",
		BaseURL:      "https://api.deepseek.com/v1",
		APIKey:       apiKey,
		DefaultModel: DefaultModel,
	}, client)
}

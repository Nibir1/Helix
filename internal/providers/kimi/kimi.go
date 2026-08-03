// internal/providers/kimi/kimi.go
// Purpose: Kimi provider wrapper.
package kimi

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// New creates a Kimi provider.
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "kimi",
		DisplayName:  "Kimi",
		BaseURL:      "https://api.moonshot.ai/v1",
		APIKey:       apiKey,
		DefaultModel: "kimi-k3",
	}, client)
}

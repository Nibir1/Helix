// internal/providers/deepseek/deepseek.go
// Purpose: DeepSeek provider wrapper.
package deepseek

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// New creates a DeepSeek provider.
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "deepseek",
		DisplayName:  "DeepSeek",
		BaseURL:      "https://api.deepseek.com/v1",
		APIKey:       apiKey,
		DefaultModel: "deepseek-v4-flash",
	}, client)
}

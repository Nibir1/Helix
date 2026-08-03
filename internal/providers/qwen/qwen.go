// internal/providers/qwen/qwen.go
// Purpose: Qwen provider wrapper.
package qwen

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// New creates a Qwen provider.
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "qwen",
		DisplayName:  "Qwen",
		BaseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		APIKey:       apiKey,
		DefaultModel: "qwen3.7-plus",
	}, client)
}

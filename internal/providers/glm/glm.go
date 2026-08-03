// internal/providers/glm/glm.go
// Purpose: GLM provider wrapper.
package glm

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// New creates a GLM provider.
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "glm",
		DisplayName:  "GLM",
		BaseURL:      "https://api.z.ai/api/paas/v4",
		APIKey:       apiKey,
		DefaultModel: "glm-5.2",
	}, client)
}

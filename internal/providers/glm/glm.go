// internal/providers/glm/glm.go
// Purpose: GLM provider wrapper.
package glm

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// DefaultModel is the natively multimodal GLM-5 build.
//
// glm-5.2 — the previous default — is text only, and Z.ai say so plainly: it
// cannot process images, screenshots or any visual content. glm-5.3-flash keeps
// the 1M-token context and tool calling, and adds image and video input, so it
// is the vision-capable default rather than a trade.
const DefaultModel = "glm-5.3-flash"

// New creates a GLM provider.
func New(apiKey string, client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "glm",
		DisplayName:  "GLM",
		BaseURL:      "https://api.z.ai/api/paas/v4",
		APIKey:       apiKey,
		DefaultModel: DefaultModel,
	}, client)
}

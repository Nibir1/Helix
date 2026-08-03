// internal/providers/llamacpp/llamacpp.go
// Purpose: llama.cpp server provider adapter.
package llamacpp

import (
	"helix/internal/providers"
	openaicompatible "helix/internal/providers/openai_compatible"
)

// DefaultEndpoint is the local llama.cpp server endpoint used by Helix.
const DefaultEndpoint = "http://127.0.0.1:8081"

// New creates a llama.cpp provider backed by the local OpenAI-compatible server.
func New(client *providers.HTTPClient) *openaicompatible.Provider {
	return openaicompatible.New(openaicompatible.Config{
		Name:         "llamacpp",
		DisplayName:  "llama.cpp Server",
		BaseURL:      DefaultEndpoint + "/v1",
		DefaultModel: "helix-local",
		Local:        true,
	}, client)
}

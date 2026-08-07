// internal/rag/embeddings_test.go
// Purpose: Verify embedding backend resolution and model naming.
package rag

import (
	"context"
	"testing"

	"helix/internal/ollama"
)

// TestEmbeddingModelName ensures backend→model mapping is stable.
func TestEmbeddingModelName(t *testing.T) {
	if got := embeddingModelName(embOpenAI); got != "text-embedding-3-small" {
		t.Fatalf("openai model = %q", got)
	}
	if got := embeddingModelName(embOllama); got != ollama.DefaultEmbeddingModel {
		t.Fatalf("ollama model = %q", got)
	}
	if got := embeddingModelName(embNone); got != "" {
		t.Fatalf("none model = %q", got)
	}
}

// TestEnsureEmbeddingBackendNonInteractive verifies the non-interactive path
// never installs anything and stays consistent with the machine state.
func TestEnsureEmbeddingBackendNonInteractive(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	resetEmbeddingBackend()

	b := ensureEmbeddingBackend(context.Background(), false)
	if b == embOpenAI {
		t.Fatal("did not expect openai backend without a key")
	}
	if b == embOllama && !hasOllamaEmbeddingModel(context.Background(), ollama.NewClient()) {
		t.Fatal("ollama backend reported without a running embedding model")
	}
}

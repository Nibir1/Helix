// internal/rag/embeddings.go
//
// Purpose: Embedding backend resolution with a fully in-Helix Ollama fallback.
// Priority: OpenAI (only when a key exists) → local Ollama (nomic-embed-text).
// When neither exists and the session is interactive, the user is offered an
// in-shell Ollama install + model pull so embeddings never depend on OpenAI.
// Background bootstraps and scripted runs NEVER prompt.
package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/ollama"

	"github.com/fatih/color"
)

// embeddingBackend identifies the engine producing embeddings this session.
type embeddingBackend string

const (
	embNone   embeddingBackend = "none"
	embOpenAI embeddingBackend = "openai"
	embOllama embeddingBackend = "ollama"
)

// resolvedBackend caches the resolution for the session ("" = unresolved).
var resolvedBackend embeddingBackend = ""

// OnInteractivePrompt lets the live UI pause/resume around interactive prompts
// raised from the update pipeline (nil-safe).
var OnInteractivePrompt func(active bool)

// resetEmbeddingBackend forces re-resolution on the next update run.
//
// Args: none. Returns: none. Complexity: O(1).
func resetEmbeddingBackend() { resolvedBackend = "" }

// currentEmbeddingBackend returns the cached resolution, resolving cheaply
// (no installs, no prompts) on first use.
//
// Args: none. Returns: embeddingBackend. Complexity: O(1) after caching.
func currentEmbeddingBackend() embeddingBackend {
	if resolvedBackend == "" {
		resolvedBackend = ensureEmbeddingBackend(context.Background(), false)
	}
	return resolvedBackend
}

// EmbeddingsAvailable reports whether a usable backend exists right now.
//
// Args: none. Returns: bool. Complexity: O(1) after caching.
func EmbeddingsAvailable() bool { return currentEmbeddingBackend() != embNone }

// ensureEmbeddingBackend resolves the backend; when interactive and nothing
// is available, it bootstraps Ollama with explicit user consent.
//
// Args:
//   - ctx: cancellation/timeout context.
//   - interactive: whether prompting the user is allowed.
//
// Returns: the resolved embeddingBackend.
// Complexity: O(1) fast path; O(install/pull time) bootstrap path.
func ensureEmbeddingBackend(ctx context.Context, interactive bool) embeddingBackend {
	if b := resolveFast(); b != embNone {
		return b
	}
	if !interactive {
		return embNone
	}
	return bootstrapOllama(ctx)
}

// resolveFast checks backends without installing or prompting.
//
// Args: none. Returns: embeddingBackend. Complexity: O(1) local checks plus
// at most two fast localhost probes.
func resolveFast() embeddingBackend {
	if ai.HasOpenAIKey() {
		return embOpenAI
	}
	if hasOllamaEmbeddingModel(context.Background(), ollama.NewClient()) {
		return embOllama
	}
	return embNone
}

// bootstrapOllama performs the consented, in-Helix Ollama bootstrap:
// install (if missing) → ensure service → pull the embedding model.
//
// Args:
//   - ctx: cancellation/timeout context.
//
// Returns: embOllama on success, embNone on any decline/failure.
// Complexity: O(install + pull time), bounded to 25 minutes.
func bootstrapOllama(ctx context.Context) embeddingBackend {
	bctx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()

	client := ollama.NewClient()

	if !ollama.IsInstalled() {
		if !askPaused("No embedding backend found. Install Ollama for local embeddings?") {
			return embNone
		}
		if err := ollama.Install(bctx); err != nil {
			color.Yellow("Ollama installation failed: %v", err)
			return embNone
		}
	}

	if err := ollama.EnsureRunning(bctx); err != nil {
		color.Yellow("Ollama service unavailable: %v", err)
		return embNone
	}

	if hasOllamaEmbeddingModel(bctx, client) {
		return embOllama
	}

	if !askPaused(fmt.Sprintf("Pull %s for local embeddings (~274MB)?", ollama.DefaultEmbeddingModel)) {
		return embNone
	}
	if err := client.PullModel(bctx, ollama.DefaultEmbeddingModel, func(status string) {
		// Route pull progress into the live bar, never raw stdout.
		notifyStage("PULLING EMBED MODEL")
	}); err != nil {
		color.Yellow("Embedding model pull failed: %v", err)
		return embNone
	}
	return embOllama
}

// hasOllamaEmbeddingModel reports whether a running Ollama daemon already has
// the embedding model installed.
//
// Args:
//   - ctx: cancellation/timeout context.
//   - client: Ollama client.
//
// Returns: bool. Complexity: O(1) localhost probes.
func hasOllamaEmbeddingModel(ctx context.Context, client *ollama.Client) bool {
	hctx, hcancel := context.WithTimeout(ctx, 3*time.Second)
	defer hcancel()
	if err := client.Health(hctx); err != nil {
		return false
	}

	lctx, lcancel := context.WithTimeout(ctx, 5*time.Second)
	defer lcancel()
	models, err := client.ListModels(lctx)
	if err != nil {
		return false
	}
	want := strings.ToLower(ollama.DefaultEmbeddingModel)
	for _, m := range models {
		id := strings.ToLower(m.ID)
		if id == want || strings.HasPrefix(id, want+":") {
			return true
		}
	}
	return false
}

// askPaused runs a confirmation prompt with the live progress bar paused.
//
// Args:
//   - question: prompt text.
//
// Returns: bool user answer. Complexity: O(1) plus user think time.
func askPaused(question string) bool {
	if OnInteractivePrompt != nil {
		OnInteractivePrompt(true)
	}
	ok := commands.AskForConfirmation(question)
	if OnInteractivePrompt != nil {
		OnInteractivePrompt(false)
	}
	return ok
}

// embeddingModelName maps a backend to the model name stored alongside each
// embedding vector (prevents ever mixing OpenAI and Ollama vectors).
//
// Args:
//   - b: resolved backend.
//
// Returns: model name ("" for none). Complexity: O(1).
func embeddingModelName(b embeddingBackend) string {
	switch b {
	case embOpenAI:
		return "text-embedding-3-small"
	case embOllama:
		return ollama.DefaultEmbeddingModel
	}
	return ""
}

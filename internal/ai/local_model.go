// internal/ai/local_model.go
// Purpose: resolve the REAL model a local runtime is serving, so a placeholder
// label never decides Helix's behavior.
//
// The problem this fixes: llama.cpp's adapter carries DefaultModel
// "local-gguf". That is honest as a UI label — llama-server serves whichever
// GGUF it was launched with and ignores the model field on the request — but the
// same string is also the key Helix uses to look up model CAPABILITIES. So with
// the placeholder active:
//
//   - GetContextLimit("local-gguf") falls through to the 8k default, and
//     GetSafeContentLimit then clamps retrieved context to a fraction of what a
//     128k-context local model could take;
//   - SupportsVision("llamacpp", "local-gguf") is false, so /eyes on refused
//     even when llama-server had a Qwen2.5-VL or a Gemma 3 GGUF loaded;
//   - /provider-status and /status printed "local-gguf", which tells the user
//     nothing about what is actually answering.
//
// llama-server DOES report the loaded model on /v1/models. Asking once, at
// selection time, replaces the placeholder with the real name and every one of
// those decisions becomes correct.
package ai

import (
	"context"
	"strings"
	"time"
)

// localModelPlaceholders are labels that stand in for "whatever this runtime
// has loaded". They are display strings, never capability keys.
var localModelPlaceholders = map[string]bool{
	"local-gguf": true,
	"local":      true,
	"":           true,
}

// IsPlaceholderModel reports whether a model name is a stand-in rather than a
// real model identifier.
func IsPlaceholderModel(model string) bool {
	return localModelPlaceholders[strings.ToLower(strings.TrimSpace(model))]
}

// ResolveActiveLocalModel replaces a placeholder active model with the real one
// the local runtime reports, returning the resolved name and whether it changed.
//
// It is a no-op unless the active provider is LOCAL and the active model is a
// placeholder: a user who deliberately picked a model keeps it, and a cloud
// provider's model list is not a statement about what is loaded anywhere.
//
// A failure to reach the runtime is not an error worth propagating — the caller
// already has, or is about to get, a reachability diagnosis from the health
// check. Here it just means the placeholder stands.
func ResolveActiveLocalModel(ctx context.Context) (string, bool) {
	if activeProvider == nil || !activeProvider.IsLocal() {
		return activeModel, false
	}
	if !IsPlaceholderModel(activeModel) {
		return activeModel, false
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	models, err := activeProvider.ListModels(ctx)
	if err != nil || len(models) == 0 {
		return activeModel, false
	}

	// llama-server reports exactly one entry: the loaded GGUF. A runtime that
	// reports several (llama-server --models-dir, LM Studio) has no way to tell
	// us which is "current", so take the first and let the user override with
	// /model use — better than continuing to reason about a placeholder.
	resolved := strings.TrimSpace(models[0].ID)
	if resolved == "" || IsPlaceholderModel(resolved) {
		return activeModel, false
	}

	activeModel = resolved
	return resolved, true
}

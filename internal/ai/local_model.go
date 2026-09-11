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

	"helix/internal/providers"
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

// ModelVerdict is what a reconcile concluded about the saved model.
type ModelVerdict int

const (
	// ModelOK: the provider still lists it, or there was nothing to check.
	ModelOK ModelVerdict = iota

	// ModelGone: the provider has a catalogue and the saved model is not in
	// it. The only verdict that repairs.
	ModelGone

	// ModelUnverifiable: no catalogue could be obtained. NOT evidence of
	// anything.
	//
	// The distinction matters for the same reason verifyProviderKey has a
	// three-way verdict: a dropped connection, an expired key or an offline
	// laptop are not a vendor retiring a model, and repairing on that evidence
	// would replace a working configuration with a guess.
	ModelUnverifiable
)

// ReconcileActiveModel checks the active model against what the provider
// actually serves, repairing it only on real evidence.
//
// Runs in the background at startup, where ResolveActiveLocalModel already
// ran, because the problem is the same shape: a saved model that does not
// describe what the provider will accept. It generalises that function rather
// than sitting beside it, so there is one answer to "is the saved model
// usable" instead of two that can disagree.
//
// What this fixes: nothing validated cfg.ProviderModel. A vendor retiring a
// model meant the ID went on the wire, came back 404, the failover breaker
// ignored it by design, every HealthCheck was a ListModels call that said
// nothing about the selected model, and no code path ever rewrote the saved
// value. Every turn failed, forever, while /provider-status reported ok.
//
// Returns the model to use, whether it changed, and why.
func ReconcileActiveModel(ctx context.Context) (string, bool, ModelVerdict) {
	if activeProvider == nil {
		return activeModel, false, ModelOK
	}
	// The placeholder case first, unchanged: for a local runtime a placeholder
	// is not a stale model, it is a label that has to be asked about.
	if resolved, changed := ResolveActiveLocalModel(ctx); changed {
		return resolved, true, ModelOK
	}

	name := activeProvider.Name()
	if activeModel == "" {
		// Nothing selected. Pick the best known model rather than leaving the
		// shell unable to answer anything.
		if pick := PreferredModel(name); pick != "" {
			activeModel = pick
			return pick, true, ModelOK
		}
		return activeModel, false, ModelUnverifiable
	}

	cache := providers.Models()
	if cache.Fresh(name) {
		// Zero network. A fresh list is as good an answer as a new request.
		return verdictFromList(name, cache)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
	}
	models, err := activeProvider.ListModels(ctx)
	if err != nil || len(models) == 0 {
		// Unreachable, unauthorised, or an empty catalogue. Keep what we have.
		return activeModel, false, ModelUnverifiable
	}
	_ = cache.Put(name, models)
	return verdictFromList(name, cache)
}

// verdictFromList judges the active model against a known catalogue.
//
// Absence only WARRANTS a repair here because the caller has established there
// IS a catalogue. Even then it is not proof: Anthropic omits some aliases,
// Ollama's /api/tags lists only pulled models, and llama.cpp reports one
// entry — which is why local providers are left alone and why the wire-level
// not-found signal is the other, stronger trigger.
func verdictFromList(provider string, cache *providers.ModelCache) (string, bool, ModelVerdict) {
	if !cache.Lists(provider) {
		return activeModel, false, ModelUnverifiable
	}
	if cache.Knows(provider, activeModel) {
		return activeModel, false, ModelOK
	}
	if activeProvider != nil && activeProvider.IsLocal() {
		// A model that is not pulled is a pull away, not a retirement. The
		// interactive path offers to fetch it; silently switching would pick a
		// different brain than the user asked for.
		return activeModel, false, ModelUnverifiable
	}
	pick := PreferredModel(provider)
	if pick == "" || pick == activeModel {
		return activeModel, false, ModelUnverifiable
	}
	activeModel = pick
	return pick, true, ModelGone
}

// NoteModelNotFound records that the wire said the active model is gone.
//
// Stronger evidence than absence from a listing, and it arrives at the worst
// moment — mid-turn — so it does not repair here. It invalidates the cached
// list that offered the model, which is provably behind, so the next reconcile
// refetches and repairs instead of re-failing on the same stale answer.
func NoteModelNotFound(provider, model string) {
	if strings.TrimSpace(provider) == "" {
		return
	}
	providers.Models().Invalidate(provider)
	_ = model
}

package ai

import (
	"context"
	"testing"
	"time"

	"helix/internal/providers"
)

func TestIsPlaceholderModel(t *testing.T) {
	placeholders := []string{"local-gguf", "LOCAL-GGUF", " local ", ""}
	for _, m := range placeholders {
		if !IsPlaceholderModel(m) {
			t.Errorf("IsPlaceholderModel(%q) = false, want true", m)
		}
	}
	// A real model name must never be treated as a placeholder, or resolution
	// would overwrite a deliberate choice.
	real := []string{"qwen2.5-vl-7b-instruct-q4_k_m", "gpt-4o", "gemma4:e2b", "llama3.1:8b"}
	for _, m := range real {
		if IsPlaceholderModel(m) {
			t.Errorf("IsPlaceholderModel(%q) = true, want false", m)
		}
	}
}

// TestPlaceholderModelBreaksCapabilityLookups documents WHY resolution matters:
// with the placeholder active, both capability questions Helix asks get the
// wrong answer.
func TestPlaceholderModelBreaksCapabilityLookups(t *testing.T) {
	const placeholder = "local-gguf"
	const realVisionModel = "qwen2.5-vl-7b-instruct"

	if got := providers.GetContextLimit(placeholder); got != 8_192 {
		t.Errorf("placeholder context limit = %d; the test's premise has changed", got)
	}
	if providers.SupportsVision("llamacpp", placeholder) {
		t.Error("the placeholder must not claim vision support")
	}
	// The same runtime, once resolved, answers both correctly.
	if !providers.SupportsVision("llamacpp", realVisionModel) {
		t.Error("a resolved multimodal model must report vision support")
	}
	if got := providers.GetContextLimit("llama3.1:8b"); got == 8_192 {
		t.Error("a resolved model should get its real context limit, not the default")
	}
}

func TestResolveActiveLocalModelNoProvider(t *testing.T) {
	saved, savedModel := activeProvider, activeModel
	t.Cleanup(func() { activeProvider, activeModel = saved, savedModel })

	activeProvider = nil
	activeModel = "local-gguf"
	got, changed := ResolveActiveLocalModel(testContext())
	if changed {
		t.Error("with no provider there is nothing to resolve")
	}
	if got != "local-gguf" {
		t.Errorf("model = %q, want the placeholder unchanged", got)
	}
}

func testContext() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	// The call under test returns before this fires; cancelling on a timer keeps
	// the test from leaking a context if that ever stops being true.
	time.AfterFunc(2*time.Second, cancel)
	return ctx
}

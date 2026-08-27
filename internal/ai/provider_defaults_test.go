// internal/ai/provider_defaults_test.go
//
// Purpose: pin the invariant that made /eyes reachable — every provider Helix
// registers must DEFAULT to a model that can see.
//
// This is the drift guard for a promise the README makes and the first-run menu
// implies. Vision is a per-MODEL property, so a provider whose default is
// text-only leaves the Phase 5 camera path refusing with "No vision-capable
// model is configured" on a correctly configured, fully paid-up account — the
// exact failure SupportsVision was widened to remove. Two of the defaults were
// text-only when this test was written (glm-5.2, deepseek-v4-flash), which is
// why it is a test and not a comment.
package ai

import (
	"testing"

	"helix/internal/providers"
	"helix/internal/providers/llamacpp"
)

func TestEveryRegisteredProviderDefaultsToAModelThatCanSee(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := InitProviders(ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}

	for _, name := range ListProviders() {
		// llama.cpp is the one honest exception: `local-gguf` is a UI label, not
		// a routing key — llama-server serves whatever GGUF was loaded by hand,
		// and Helix cannot know whether that build has a vision projector.
		if name == llamacpp.Name {
			continue
		}
		model := DefaultModelForProvider(name)
		if model == "" {
			t.Errorf("%s has no default model", name)
			continue
		}
		if !providers.SupportsVision(name, model) {
			t.Errorf("%s defaults to %q, which cannot process images; "+
				"pick the vendor's multimodal build so /blackbox eyes on works "+
				"on a fresh key", name, model)
		}
	}
}

// The two providers added alongside the vision defaults must actually be
// registered — a menu entry for a provider the registry does not know is the
// drift that shipped llamacpp broken once already.
func TestGeminiAndMetaAreRegistered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := InitProviders(ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}

	for _, tc := range []struct{ name, model string }{
		{"gemini", "gemini-3.7-flash"},
		{"meta", "muse-spark-1.2"},
	} {
		p, err := GetProviderByName(tc.name)
		if err != nil {
			t.Errorf("%s must be registered: %v", tc.name, err)
			continue
		}
		if got := p.DefaultModel(); got != tc.model {
			t.Errorf("%s default model = %q, want %q", tc.name, got, tc.model)
		}
		if !p.RequiresAPIKey() {
			t.Errorf("%s is a cloud provider and must require a key", tc.name)
		}
		// Both vendors document function calling; without the flag the planner
		// never attempts a tool call and silently runs on the prompt ladder.
		if !p.Capabilities().ToolUse {
			t.Errorf("%s should advertise tool use", tc.name)
		}
		if !p.Capabilities().Vision {
			t.Errorf("%s should advertise vision on its default model", tc.name)
		}
	}
}

// A large context window is not decoration: GetSafeContentLimit derives the RAG
// budget from it, so a model missing from the catalogue is silently starved to
// ~4k characters regardless of what it can actually take.
func TestNewProviderDefaultsHaveCataloguedContextWindows(t *testing.T) {
	for model, want := range map[string]int{
		"gemini-3.7-flash":             1_000_000,
		"muse-spark-1.2":               1_048_576,
		"glm-5.3-flash":                1_048_576,
		"gpt-5.6-luna":                 1_050_000,
		"deepseek-v4-flash-vision-exp": 1_000_000,
	} {
		if got := providers.GetContextLimit(model); got != want {
			t.Errorf("GetContextLimit(%q) = %d, want %d", model, got, want)
		}
	}
}

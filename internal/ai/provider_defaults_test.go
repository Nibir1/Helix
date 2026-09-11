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

// INVERTED, and this inversion is the drift guard for the whole change.
//
// It used to assert that every provider hardcodes a default model that can
// see. That invariant was protecting a real promise — /blackbox eyes on must
// work on a fresh key — but it was protecting it with compiled-in model IDs,
// which is precisely the thing that rots: vendors retire models, nothing
// validated the saved ID, and a retired default made every turn fail with an
// unexplained 404 while /provider-status still reported ok.
//
// So the assertion flips. No provider may ship a model ID, and a new adapter
// that reintroduces one fails HERE. The promise it used to protect is now
// protected by TestRankingPutsAVisionModelFirstForEveryVendor, which asserts
// the same thing against a live catalogue instead of a constant.
func TestNoProviderShipsAHardcodedDefaultModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // os.UserHomeDir on Windows
	providers.ResetModelCacheForTest()
	t.Cleanup(providers.ResetModelCacheForTest)

	if err := InitProviders(ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}

	for _, name := range ListProviders() {
		// llama.cpp is the one honest exception, and it was exempt from the
		// old assertion for the same reason: `local-gguf` is a UI label, not a
		// routing key — llama-server serves whatever GGUF was loaded by hand,
		// so there is no catalogue to resolve against. `custom` is a
		// user-supplied endpoint with the same property.
		if name == llamacpp.Name || name == "custom" {
			continue
		}
		p, err := GetProviderByName(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := p.DefaultModel(); got != "" {
			t.Errorf("%s ships the hardcoded model %q. Vendors retire models; a "+
				"compiled-in ID means a fresh install fails on a date nobody chose. "+
				"Leave it empty and let ai.PreferredModel resolve it from the "+
				"provider's live catalogue.", name, got)
		}
	}
}

// The promise the old test was really protecting: with nothing selected, a
// provider must still report that it can see, or /blackbox eyes on refuses on
// a fresh key with no explanation.
func TestProvidersAdvertiseVisionWithNoModelSelected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	providers.ResetModelCacheForTest()
	t.Cleanup(providers.ResetModelCacheForTest)

	if err := InitProviders(ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}
	for _, name := range ListProviders() {
		if name == llamacpp.Name || name == "custom" {
			continue
		}
		p, err := GetProviderByName(name)
		if err != nil {
			continue
		}
		if !p.Capabilities().Vision {
			t.Errorf("%s reports it cannot see before a model is chosen — the camera "+
				"path would refuse on a fresh key", name)
		}
	}
}

// The two providers added alongside the vision defaults must actually be
// registered — a menu entry for a provider the registry does not know is the
// drift that shipped llamacpp broken once already.
func TestGeminiAndMetaAreRegistered(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // os.UserHomeDir on Windows
	if err := InitProviders(ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}

	for _, tc := range []struct{ name string }{
		{"gemini"},
		{"meta"},
	} {
		p, err := GetProviderByName(tc.name)
		if err != nil {
			t.Errorf("%s must be registered: %v", tc.name, err)
			continue
		}
		// The exact-model assertion is GONE on purpose: pinning a live vendor
		// ID in a test is the same bet as compiling one into the adapter, and
		// it loses on the same day. Registration, key policy and capabilities
		// are the durable facts.
		if !p.RequiresAPIKey() {
			t.Errorf("%s is a cloud provider and must require a key", tc.name)
		}
		// Both vendors document function calling; without the flag the planner
		// never attempts a tool call and silently runs on the prompt ladder.
		if !p.Capabilities().ToolUse {
			t.Errorf("%s should advertise tool use", tc.name)
		}
		if !p.Capabilities().Vision {
			t.Errorf("%s should advertise vision", tc.name)
		}
	}
}

// A large context window is not decoration: GetSafeContentLimit derives the RAG
// budget from it, so a model missing from the catalogue is silently starved to
// ~4k characters regardless of what it can actually take.
//
// Renamed: these are no longer anybody's DEFAULT, they are current flagships
// whose context windows the catalogue should know. The catalogue is still worth
// having and still worth testing — it is what ranks models and sizes the RAG
// budget — it just no longer decides what a fresh install runs.
func TestCatalogKnowsCurrentFlagshipContextWindows(t *testing.T) {
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

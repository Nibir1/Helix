// internal/providers/ranking_test.go
// Purpose: the ranking's promises, over fixtures shaped like what vendors
// actually return. No network: these are pure functions over model IDs.
package providers

import "testing"

// openaiLike is a /v1/models payload with the noise a real one carries. The
// picker used to show the first 24 of these in API order, which is why
// IsChatModel matters more than the fast heuristic.
var openaiLike = []ModelInfo{
	{ID: "text-embedding-3-small"},
	{ID: "whisper-1"},
	{ID: "gpt-live-transcribe"},
	{ID: "dall-e-3"},
	{ID: "omni-moderation-latest"},
	{ID: "gpt-5.6-luna"},
	{ID: "gpt-5.6-luna-mini"},
	{ID: "gpt-4o"},
}

func ids(ms []ModelInfo) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

// Nothing the provider lists may become unreachable. Non-chat entries SINK;
// they are never dropped, because a user who wants one must still find it.
func TestRankingNeverLosesAModel(t *testing.T) {
	got := RankModels("openai", openaiLike)
	if len(got) != len(openaiLike) {
		t.Fatalf("ranking returned %d of %d models", len(got), len(openaiLike))
	}
	seen := map[string]bool{}
	for _, m := range got {
		seen[m.ID] = true
	}
	for _, m := range openaiLike {
		if !seen[m.ID] {
			t.Errorf("%q disappeared from the ranked list", m.ID)
		}
	}
}

// The owner's stated priority: "vision+flash models are first priorities".
func TestRankingPutsVisionAndFastFirst(t *testing.T) {
	got := ids(RankModels("openai", openaiLike))
	if got[0] != "gpt-5.6-luna-mini" {
		t.Errorf("first = %q, want the vision+fast build first; order: %v", got[0], got)
	}
	// Every chat model must outrank every non-chat one.
	lastChat := -1
	firstNonChat := len(got)
	for i, id := range got {
		if IsChatModel(id) {
			lastChat = i
		} else if i < firstNonChat {
			firstNonChat = i
		}
	}
	if lastChat > firstNonChat {
		t.Errorf("a non-chat model outranked a chat model: %v", got)
	}
}

// Per-vendor: whatever the list looks like, the top pick must be able to see —
// this is what preserves the /blackbox eyes promise that the old
// hardcoded-default test protected, without pinning a live model ID.
func TestRankingPutsAVisionModelFirstForEveryVendor(t *testing.T) {
	fixtures := map[string][]ModelInfo{
		"openai":    openaiLike,
		"anthropic": {{ID: "claude-haiku-5"}, {ID: "claude-opus-5"}},
		"deepseek":  {{ID: "deepseek-chat"}, {ID: "deepseek-v4-flash-vision-exp"}},
		"gemini":    {{ID: "gemini-3.7-flash"}, {ID: "text-embedding-004"}},
		"glm":       {{ID: "glm-5.3"}, {ID: "glm-5.3-flash"}},
		"xai":       {{ID: "grok-build"}, {ID: "grok-4.6"}},
		"ollama":    {{ID: "nomic-embed-text"}, {ID: "gemma4:e2b"}},
	}
	for provider, models := range fixtures {
		top := RankModels(provider, models)[0]
		if !SupportsVision(provider, top.ID) {
			t.Errorf("%s: top pick %q cannot see — /blackbox eyes on would refuse on a "+
				"fresh key", provider, top.ID)
		}
		if !IsChatModel(top.ID) {
			t.Errorf("%s: top pick %q is not a chat model", provider, top.ID)
		}
	}
}

// An -exp or -preview suffix must NOT be demoted: DeepSeek's own current
// vision model is deepseek-v4-flash-vision-exp, so a "prefer stable" rule
// would demote exactly the model the user wants first.
func TestExperimentalBuildsAreNotDemoted(t *testing.T) {
	got := ids(RankModels("deepseek", []ModelInfo{
		{ID: "deepseek-chat"},
		{ID: "deepseek-v4-flash-vision-exp"},
	}))
	if got[0] != "deepseek-v4-flash-vision-exp" {
		t.Errorf("order = %v, want the -exp vision build first", got)
	}
}

func TestIsChatModelFailsOpen(t *testing.T) {
	for _, id := range []string{"some-future-flagship", "brand-new-thing-9"} {
		if !IsChatModel(id) {
			t.Errorf("%q was rejected — a new naming scheme must not make a flagship "+
				"invisible", id)
		}
	}
	for _, id := range []string{
		"text-embedding-3-small", "whisper-1", "gpt-live-transcribe",
		"dall-e-3", "omni-moderation-latest", "",
	} {
		if IsChatModel(id) {
			t.Errorf("%q counted as a chat model", id)
		}
	}
}

func TestIsFastModel(t *testing.T) {
	for _, id := range []string{
		"gemini-3.7-flash", "gpt-5.6-luna-mini", "claude-haiku-5",
		"glm-5.3-air", "gemma4:e2b", "llama3.1:8b", "qwen3-4b",
	} {
		if !IsFastModel(id) {
			t.Errorf("%q should read as a fast tier", id)
		}
	}
	for _, id := range []string{"gpt-5.6-luna", "claude-opus-5", "deepseek-v4-pro"} {
		if IsFastModel(id) {
			t.Errorf("%q should not read as a fast tier", id)
		}
	}
}

// Capability tags must not invent a context window. GetContextLimit falls back
// to 8192 for anything it has never heard of, and most live IDs now are —
// printing "8k" for all of them would be a confident lie.
func TestCapabilityTagsOmitAnUnknownContextWindow(t *testing.T) {
	for _, tag := range ModelCapabilityTags("openai", "some-future-flagship") {
		if tag == "8k" {
			t.Error("an unknown model was labelled 8k, which nobody measured")
		}
	}
	tags := ModelCapabilityTags("openai", "gpt-5.6-luna")
	var sawContext bool
	for _, tag := range tags {
		if tag != "sees" && tag != "fast" && tag != "tools" {
			sawContext = true
		}
	}
	if !sawContext {
		t.Errorf("a catalogued model lost its context tier: %v", tags)
	}
}

// Empty and duplicate input must not panic — this runs over whatever a vendor
// returns, including an empty list from a key with no access.
func TestRankingHandlesDegenerateInput(t *testing.T) {
	if got := RankModels("openai", nil); len(got) != 0 {
		t.Errorf("nil input produced %d models", len(got))
	}
	dup := []ModelInfo{{ID: "gpt-4o"}, {ID: "gpt-4o"}, {ID: ""}}
	if got := RankModels("openai", dup); len(got) != 3 {
		t.Errorf("duplicates changed the length: %d", len(got))
	}
}

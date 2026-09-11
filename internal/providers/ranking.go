// internal/providers/ranking.go
// Purpose: order a provider's live model list so the ones worth picking are at
// the top, and tell chat models apart from everything else a /models endpoint
// returns.
//
// WHY THIS EXISTS. Helix used to hardcode one model ID per provider and show
// the first 24 entries of the vendor's list in whatever order the API returned
// them. Both parts aged badly. Vendors retire models — "deepseek and other AI
// providers constantly removing their old AI models" — so a compiled-in
// default is a time bomb, and a list in API order buries the multimodal flash
// build under a dozen embedding and speech endpoints.
//
// WHAT "FIRST PRIORITY" MEANS HERE, in the owner's words: "vision+flash models
// are first priorities". So the sort is vision, then fast, and only then the
// things a machine would naturally rank by (tool use, context size).
//
// WHY SUBSTRINGS. Same reason visionModelSubstrings in catalog.go uses them:
// vendors bury the marker mid-ID (`gemini-3.7-flash`, `claude-haiku-5`,
// `gpt-5.6-luna-mini`), and a prefix table would need an entry per release.
// It is a heuristic and it will occasionally be wrong, which is exactly why
// the picker also accepts any ID typed verbatim.
package providers

import (
	"sort"
	"strings"
)

// fastModelSubstrings mark a model the vendor sells as the quick, cheap tier.
//
// The size suffixes are here because local builds express the same idea as a
// parameter count rather than a word: a 4B model is the "flash" of an Ollama
// install.
var fastModelSubstrings = []string{
	"flash", "mini", "nano", "lite", "instant", "turbo", "haiku",
	"small", "fast", "air", "swift",
	"e2b", "e4b", "-1b", "-2b", "-3b", "-4b", "-7b", "-8b",
	":1b", ":2b", ":3b", ":4b", ":7b", ":8b",
}

// nonChatSubstrings mark an entry that is not a conversational model.
//
// This matters MORE than the fast list. OpenAI's /v1/models returns dozens of
// embedding, speech, image and moderation endpoints, and the picker used to
// show the first 24 in API order — so the list a user chose their brain from
// was mostly things that cannot hold a conversation. Ranking without this
// would sort that noise, not remove it.
//
// "realtime" and "transcribe" are here deliberately: gpt-live-transcribe is a
// speech model and belongs in the STT chain, not in the brain picker.
var nonChatSubstrings = []string{
	"embed", "embedding", "whisper", "-tts", "tts-", "text-to-speech",
	"-audio", "audio-", "transcribe", "realtime", "speech",
	"dall-e", "-image", "image-", "imagen", "-vision-preview-only",
	"moderation", "rerank", "guard", "-edit", "similarity", "search-query",
	"search-document", "code-search",
}

// IsFastModel reports whether the ID looks like a vendor's quick tier.
func IsFastModel(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	for _, s := range fastModelSubstrings {
		if strings.Contains(id, s) {
			return true
		}
	}
	return false
}

// IsChatModel reports whether the ID looks like something that can hold a
// conversation.
//
// Fails OPEN: an ID matching nothing in either table is treated as a chat
// model, because a vendor shipping a new naming scheme must not make its
// flagship invisible. The cost of a false positive is one unusable row in a
// list; the cost of a false negative is a model the user cannot reach.
func IsChatModel(modelID string) bool {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if id == "" {
		return false
	}
	for _, s := range nonChatSubstrings {
		if strings.Contains(id, s) {
			return false
		}
	}
	return true
}

// contextTier buckets a context window so unknown models do not sort against
// each other on a number none of them really has.
//
// GetContextLimit returns DefaultContextLimit for anything it does not
// recognise, and with model IDs now coming from a live list rather than a
// curated table, most of them will be unrecognised. Sorting on the raw number
// would order every unknown model identically anyway while implying a
// precision that is not there.
func contextTier(modelID string) int {
	switch n := GetContextLimit(modelID); {
	case n >= 1_000_000:
		return 4
	case n >= 200_000:
		return 3
	case n >= 100_000:
		return 2
	case n > DefaultContextLimit:
		return 1
	default:
		return 0
	}
}

// RankModels orders a model list best-first for picking a brain.
//
// Never filters — len(out) == len(in), always. A model the provider lists must
// stay reachable even if every heuristic here mis-reads it; non-chat entries
// sink to the bottom rather than disappearing, so a user who genuinely wants
// one can still find it.
//
// Stable, so two models the heuristics cannot separate keep the order the
// vendor returned them in.
func RankModels(provider string, in []ModelInfo) []ModelInfo {
	out := make([]ModelInfo, len(in))
	copy(out, in)

	score := func(m ModelInfo) [5]int {
		return [5]int{
			boolRank(IsChatModel(m.ID)),
			boolRank(SupportsVision(provider, m.ID)),
			boolRank(IsFastModel(m.ID)),
			boolRank(SupportsToolUse(provider, m.ID)),
			contextTier(m.ID),
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := score(out[i]), score(out[j])
		for k := range a {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		// Everything the heuristics can see is equal. Descending ID is a crude
		// stand-in for "newer", and it is deliberately last: it puts
		// gpt-5.6-luna above gpt-4o without pretending to parse versions.
		return out[i].ID > out[j].ID
	})
	return out
}

// boolRank turns a capability into a sort key where true wins.
func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ModelCapabilityTags renders the short capability list shown beside a model.
//
// The context tier is omitted when GetContextLimit fell back to its default:
// printing "8k" for every model the catalog has never heard of would be a
// confident lie about a number nobody measured.
func ModelCapabilityTags(provider, modelID string) []string {
	tags := []string{}
	if SupportsVision(provider, modelID) {
		tags = append(tags, "sees")
	}
	if IsFastModel(modelID) {
		tags = append(tags, "fast")
	}
	if SupportsToolUse(provider, modelID) {
		tags = append(tags, "tools")
	}
	if n := GetContextLimit(modelID); n > DefaultContextLimit {
		tags = append(tags, humanContext(n))
	}
	if !IsChatModel(modelID) {
		tags = append(tags, "not a chat model")
	}
	return tags
}

// humanContext renders a context window the way a person says it.
func humanContext(n int) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(itoaDiv(n, 1_000_000), ".0") + "M"
	default:
		return itoaDiv(n, 1_000) + "k"
	}
}

// itoaDiv renders n/div with one decimal place, trimming a trailing ".0".
func itoaDiv(n, div int) string {
	whole := n / div
	frac := (n % div) * 10 / div
	if frac == 0 {
		return itoa(whole)
	}
	return itoa(whole) + "." + itoa(frac)
}

// itoa is strconv.Itoa without the import, kept local so this file stays
// dependency-free alongside catalog.go.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

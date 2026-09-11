// cmd/helix/voice_presets_test.go
// Purpose: P9.7 — keep the recommended chains honest. A preset is a promise
// that a pick will work, so the thing worth testing is that every provider and
// model it names actually exists, and that the local one stays local.
package main

import (
	"strings"
	"testing"

	"helix/internal/speech"
)

// Every preset must name providers the registry actually has and models the
// pricing catalog actually lists. Without this, renaming a catalog entry leaves
// a preset pointing at a model no provider serves — and the failure would land
// on the user as an HTTP 400 after the wizard said "configured".
func TestPresetsMatchCatalogAndRegistry(t *testing.T) {
	catalog, err := speech.LoadCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	has := func(kind, provider, model string) bool {
		for _, e := range catalog {
			if e.Kind == kind && e.Provider == provider {
				if model == "" || e.Model == model {
					return true
				}
			}
		}
		return false
	}

	for _, p := range speechPresets() {
		if p.Name == "" || p.Note == "" {
			t.Errorf("preset %+v must carry a name and a reason to pick it", p)
		}
		if !has("stt", p.STTProvider, p.STTModel) {
			t.Errorf("preset %q names STT %s/%s which is not in the catalog",
				p.Name, p.STTProvider, p.STTModel)
		}
		if !has("tts", p.TTSProvider, p.TTSModel) {
			t.Errorf("preset %q names TTS %s/%s which is not in the catalog",
				p.Name, p.TTSProvider, p.TTSModel)
		}
		for _, f := range p.STTFallbacks {
			if !has("stt", f, "") {
				t.Errorf("preset %q names unknown STT fallback %q", p.Name, f)
			}
		}
		for _, f := range p.TTSFallbacks {
			if !has("tts", f, "") {
				t.Errorf("preset %q names unknown TTS fallback %q", p.Name, f)
			}
		}
	}
}

// A cloud preset's fallback must be LOCAL. The point of a fallback is surviving
// the most likely failure — the network — and a second cloud vendor does not.
func TestCloudPresetsFallBackToLocal(t *testing.T) {
	for _, p := range speechPresets() {
		if !p.needsKey() {
			continue
		}
		if len(p.STTFallbacks) == 0 || len(p.TTSFallbacks) == 0 {
			t.Errorf("cloud preset %q must carry a fallback in both directions", p.Name)
			continue
		}
		for _, f := range append(p.STTFallbacks, p.TTSFallbacks...) {
			if !isLocalSpeechProvider(f) {
				t.Errorf("preset %q falls back to %q, which is not local — a second "+
					"cloud vendor does not survive a dropped network", p.Name, f)
			}
		}
	}
}

// Every keyless preset must stay private: no cloud provider anywhere in the
// chain — primary OR fallback — and nothing needing a container runtime
// (ADR-002 amendment).
//
// ASSERTION WIDENED (2026-08-23), on intent rather than convenience. This used
// to find the first keyless preset and require it to have NO fallbacks at all,
// which was an accurate proxy while exactly one such preset existed and it had
// none. Adding the CSM chain broke that proxy without touching the rule it
// stood for: CSM falls back to piper-local, which is local, and the fallback is
// the point — CSM needs a GPU, so on a machine without one the chain has to
// degrade to a voice that works rather than to nothing. What must never happen
// is a *cloud* fallback on a chain someone chose for privacy, and that is now
// asserted directly, for every keyless preset rather than just the first.
func TestLocalPresetsAreFullyLocalAndDockerFree(t *testing.T) {
	presets := speechPresets()

	var keyless int
	for _, p := range presets {
		if p.needsKey() {
			continue
		}
		keyless++

		for _, name := range append([]string{p.STTProvider, p.TTSProvider},
			append(p.STTFallbacks, p.TTSFallbacks...)...) {
			if name == "" {
				continue
			}
			if !isLocalSpeechProvider(name) {
				t.Errorf("preset %q reaches a cloud provider %q — that would quietly "+
					"undo the reason someone picked a private chain", p.Name, name)
			}
			// Kokoro is the one voice that needs Docker; the docker-free
			// guarantee is the promise being made here.
			if strings.Contains(name, "kokoro") {
				t.Errorf("preset %q requires a container runtime via %q", p.Name, name)
			}
		}
	}

	if keyless == 0 {
		t.Fatal("there must be at least one fully-local preset (ADR-012)")
	}
}

// Local providers must persist an EMPTY model: their display text ("piper
// (sidecar)") is not a model identifier, and sending it is a 400.
func TestPresetLocalProvidersPersistNoModel(t *testing.T) {
	catalog, err := speech.LoadCatalog()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if got := presetAPIModel(catalog, "tts", "piper-local", "piper (sidecar)"); got != "" {
		t.Fatalf("local provider model must be empty, got %q", got)
	}
	if got := presetAPIModel(catalog, "stt", "whisper-local", ""); got != "" {
		t.Fatalf("local provider model must be empty, got %q", got)
	}
	if got := presetAPIModel(catalog, "stt", "groq", "whisper-large-v3-turbo"); got != "whisper-large-v3-turbo" {
		t.Fatalf("cloud provider must keep its model, got %q", got)
	}
}

// availablePresets must never offer a chain this build cannot select, and must
// drop an unregistered fallback rather than the whole preset.
func TestAvailablePresetsFilterUnregisteredProviders(t *testing.T) {
	// Only the local providers registered: the two cloud presets must vanish.
	got := availablePresets([]string{"whisper-local"}, []string{"piper-local"})
	if len(got) != 1 {
		t.Fatalf("expected only the local preset to survive, got %d: %+v", len(got), got)
	}
	if got[0].needsKey() {
		t.Fatalf("surviving preset should be the local one, got %q", got[0].Name)
	}

	// Cloud primaries registered but no local sidecars: the presets survive
	// with their fallbacks stripped, because a missing fallback is a weaker
	// chain, not an invalid one.
	got = availablePresets([]string{"groq", "deepgram"}, []string{"openai", "deepgram"})
	if len(got) != 2 {
		t.Fatalf("expected both cloud presets, got %d: %+v", len(got), got)
	}
	for _, p := range got {
		if len(p.STTFallbacks) != 0 || len(p.TTSFallbacks) != 0 {
			t.Errorf("preset %q kept an unregistered fallback: %+v", p.Name, p)
		}
	}
}

// The menu must always offer a way out to the full tables, or a user whose
// provider is not in a preset is stuck.
func TestPresetMenuAlwaysOffersManualChoice(t *testing.T) {
	presets := speechPresets()
	items := presetMenuItems(presets)
	if len(items) != len(presets)+1 {
		t.Fatalf("menu must add exactly one manual entry, got %d items for %d presets",
			len(items), len(presets))
	}
	last := items[len(items)-1]
	if !strings.Contains(strings.ToLower(last.Label), "manual") {
		t.Fatalf("last menu entry must be the manual escape hatch, got %q", last.Label)
	}
	// Tag != "" is not enough, and asserting only that is what let the bug
	// through: "recommended" was assigned first and then overwritten by
	// "needs a key", so the recommendation never rendered while its endorsement
	// COLOUR stayed on, painting a precondition green.
	if !items[0].Good {
		t.Error("the first preset should be badged as an endorsement")
	}
	if !strings.Contains(items[0].Tag, "recommended") {
		t.Errorf("the first preset must actually say it is recommended, got tag %q", items[0].Tag)
	}
	// A precondition must survive being combined with the recommendation.
	if presets[0].needsKey() && !strings.Contains(items[0].Tag, "needs a key") {
		t.Errorf("the recommendation must not swallow the precondition, got tag %q", items[0].Tag)
	}
	// Every preset with a precondition must show it.
	for i, p := range presets {
		if p.Tag != "" && !strings.Contains(items[i].Tag, p.Tag) {
			t.Errorf("preset %q must surface its precondition %q, got tag %q",
				p.Name, p.Tag, items[i].Tag)
		}
	}
}

// The duplex preset has to be REACHABLE. availablePresets drops any preset
// whose providers this build does not register, and a duplex chain is the one
// shape where that filter could plausibly be wrong.
func TestDuplexPresetSurvivesTheAvailabilityFilter(t *testing.T) {
	stt := []string{"openai", "whisper-local", "groq", "deepgram"}
	tts := []string{"openai", "piper-local", "deepgram", "csm-local"}
	var found bool
	for _, p := range availablePresets(stt, tts) {
		if p.Duplex {
			found = true
		}
	}
	if !found {
		t.Fatal("the duplex preset is filtered out of the menu, so the only way to " +
			"select full duplex is the manual table it was added to avoid")
	}
}

// A duplex preset MUST still configure TTS. gpt-live-1 replaces the chain for
// the life of a SESSION, not the life of the config — `/blackbox say`, an
// unprompted remark before the first turn, and every turn on a machine with no
// libopus all still need a voice.
func TestTheDuplexPresetStillConfiguresAVoice(t *testing.T) {
	p := duplexPreset(t)
	if p.TTSProvider == "" {
		t.Error("the duplex preset leaves TTS blank; Helix would go silent whenever it " +
			"is not mid-conversation, and on any machine where the session cannot open")
	}
	if len(p.TTSFallbacks) == 0 {
		t.Error("the duplex preset has no TTS fallback, unlike every other cloud preset")
	}
}

// And it MUST carry an STT fallback, because its primary model cannot
// transcribe a clip. Without one, a machine with no libopus has no ears at all.
func TestTheDuplexPresetFallsBackToSomethingThatCanTranscribe(t *testing.T) {
	p := duplexPreset(t)
	if len(p.STTFallbacks) == 0 {
		t.Fatal("the duplex preset has no STT fallback; on a machine without libopus " +
			"its primary model cannot transcribe anything and the turn has nowhere to go")
	}
	for _, f := range p.STTFallbacks {
		if speech.IsDuplexOnlyModel(f) {
			t.Errorf("STT fallback %q is itself duplex-only", f)
		}
	}
}

// The summary is what the user reads immediately after choosing. Describing a
// duplex chain as two independent halves is the one thing it must not do.
func TestTheDuplexSummaryDescribesOneSession(t *testing.T) {
	got := duplexPreset(t).presetSummary()
	if strings.Contains(got, "hears you with") {
		t.Errorf("the duplex summary uses the two-halves wording: %q", got)
	}
	for _, want := range []string{"one live session", "gpt-live-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q does not mention %q", got, want)
		}
	}
}

// Both preconditions must be visible BEFORE the choice. presetMenuItems only
// auto-adds "needs a key" when the Tag is empty, so a preset with any other
// precondition has to state both itself or silently lose one.
func TestTheDuplexPresetDeclaresBothPreconditions(t *testing.T) {
	p := duplexPreset(t)
	for _, want := range []string{"key", "libopus"} {
		if !strings.Contains(strings.ToLower(p.Tag), want) {
			t.Errorf("tag %q does not mention %q — the user finds out after paying for a session", p.Tag, want)
		}
	}
	// And the price, which is the other thing you cannot discover from the menu.
	if !strings.Contains(p.Note, "$0.05") {
		t.Error("the duplex preset does not say what it costs; it is the most expensive " +
			"option on the menu by a wide margin")
	}
}

// It must not be the recommended one. presetMenuItems marks index 0 "recommended",
// and a per-minute bill is not what a first-time user should be steered into.
func TestTheDuplexPresetIsNotRecommendedByDefault(t *testing.T) {
	if speechPresets()[0].Duplex {
		t.Error("the duplex preset is first, so the menu marks it 'recommended' — " +
			"that is a $0.05/min default for someone who has not chosen yet")
	}
}

func duplexPreset(t *testing.T) speechPreset {
	t.Helper()
	for _, p := range speechPresets() {
		if p.Duplex {
			return p
		}
	}
	t.Fatal("no duplex preset in this build")
	return speechPreset{}
}

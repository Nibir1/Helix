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
	if !items[0].Good || items[0].Tag == "" {
		t.Error("the first preset should be badged as the recommendation")
	}
}

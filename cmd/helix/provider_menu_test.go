// cmd/helix/provider_menu_test.go
// Purpose: the first-run menu is a curated shortest path, not the list of what
// Helix supports — and the two must not silently drift apart.
package main

import (
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/config"
	"helix/internal/providers"
	"helix/internal/providers/llamacpp"
	"helix/internal/speech"
)

// TestFirstRunMenuExcludesHandManagedRuntimes pins the demotion.
//
// llama.cpp asks the user to install a runtime, obtain a GGUF the installed
// build can actually load, and launch a server by hand — a poor thing to put in
// the choice someone makes before anything works. Ollama does the same job with
// none of it, and is itself built on llama.cpp, so the engine is present either
// way.
func TestFirstRunMenuExcludesHandManagedRuntimes(t *testing.T) {
	for _, opt := range providerOptions {
		if opt.ID == llamacpp.Name {
			t.Errorf("%s must not be in the first-run menu: selecting it there cannot "+
				"produce a working shell without several manual steps", opt.ID)
		}
		if strings.TrimSpace(opt.Label) == "" {
			t.Errorf("provider %q has no label", opt.ID)
		}
	}
	// Ollama must remain, or there is no local option at all in the menu.
	var hasOllama bool
	for _, opt := range providerOptions {
		if opt.ID == "ollama" {
			hasOllama = true
		}
	}
	if !hasOllama {
		t.Error("the menu must keep a local option")
	}
}

// TestDemotedProviderStaysUsable is the other half: demoted is not deleted. The
// edge case llama.cpp exists for (hardware Ollama cannot serve — see
// docs/edge_deployment.md) depends on it still being selectable.
func TestDemotedProviderStaysUsable(t *testing.T) {
	if err := ai.InitProviders(ai.ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}
	if !ai.HasProvider(llamacpp.Name) {
		t.Fatal("llama.cpp must stay registered so /provider use llamacpp works")
	}
	// And setupProvider must accept it, or the switch would be rejected after
	// the registry accepted it — the exact drift that shipped llamacpp broken
	// once before.
	if _, err := ai.GetProviderByName(llamacpp.Name); err != nil {
		t.Errorf("llama.cpp must be retrievable: %v", err)
	}
}

// TestAdvancedProvidersNamesWhatTheMenuOmits: a curated menu is only honest if
// it says what it left out. Derived from the registry so it cannot go stale.
func TestAdvancedProvidersNamesWhatTheMenuOmits(t *testing.T) {
	if err := ai.InitProviders(ai.ProviderSettings{}); err != nil {
		t.Fatalf("init providers: %v", err)
	}

	extra := advancedProviderNames()
	var found bool
	for _, name := range extra {
		found = found || name == llamacpp.Name
		// Nothing already in the menu may be repeated here.
		for _, opt := range providerOptions {
			if opt.ID == name {
				t.Errorf("%s is in the menu AND listed as extra", name)
			}
		}
	}
	if !found {
		t.Errorf("llama.cpp is registered but not announced as available: %v", extra)
	}
	// "custom" is an internal endpoint shim, not a provider anyone chooses.
	for _, name := range extra {
		if name == "custom" {
			t.Error("the internal custom endpoint must not be advertised")
		}
	}
}

// TestCapabilityLookupsUnaffectedByDemotion guards the thing a demotion could
// plausibly break by accident.
func TestCapabilityLookupsUnaffectedByDemotion(t *testing.T) {
	if got := providers.PreferredMaxTokensField(llamacpp.Name, "local-gguf"); got !=
		providers.FieldMaxTokens {
		t.Errorf("llama.cpp still speaks max_tokens, got %q", got)
	}
}

// TestFallbackRowUsesTheRegistryKey is the case-sensitivity bug that made every
// fallback row read "unknown".
//
// Callers pass the DISPLAY kind ("STT"), while every registry lookup keys on the
// lowercase name. speechCredentialState matched no case, returned ok=false, and
// the table fell through to its empty defaults — so a prompt built to help the
// user choose told them nothing at all.
func TestFallbackRowUsesTheRegistryKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var err error
	cfg, err = config.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		t.Fatalf("speech init: %v", err)
	}

	// A keyed cloud provider must report its cost and its key state.
	cloud := fallbackRow("tts", "openai")
	if cloud.ready == "unknown" {
		t.Error("a registered provider must not report readiness as unknown")
	}
	if cloud.cost == "—" {
		t.Error("a catalogued provider must report a cost")
	}

	// A local sidecar is free and gated on a running server, not a key.
	local := fallbackRow("tts", "kokoro-local")
	if local.cost != "free" {
		t.Errorf("a local sidecar costs nothing, got %q", local.cost)
	}
	// Environment-independent: whether kokoro happens to be running on this
	// machine decides the wording, but never the SUBJECT — a local sidecar's
	// readiness is about its server, never about a key.
	if strings.Contains(strings.ToLower(local.ready), "key") {
		t.Errorf("a local sidecar needs no key; readiness = %q", local.ready)
	}
	if local.ready == "" || local.ready == "unknown" {
		t.Errorf("a registered local sidecar must report a definite readiness, got %q", local.ready)
	}

	// The uppercase display form must not be what callers key on; if it leaks
	// through it produces exactly the "unknown" regression.
	if got := fallbackRow("TTS", "openai"); got.ready != "unknown" {
		t.Log("uppercase kind now resolves too — fine, but callers should still normalize")
	}
}

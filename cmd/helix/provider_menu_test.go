// cmd/helix/provider_menu_test.go
// Purpose: the first-run menu is a curated shortest path, not the list of what
// Helix supports — and the two must not silently drift apart.
package main

import (
	"strings"
	"testing"

	"helix/internal/ai"
	"helix/internal/providers"
	"helix/internal/providers/llamacpp"
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

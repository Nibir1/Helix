// internal/providers/providers_test.go
package providers

import (
	"path/filepath"
	"testing"
)

func TestKeyStoreSetGet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")

	ks, err := NewKeyStoreAt(path)
	if err != nil {
		t.Fatalf("failed to create keystore: %v", err)
	}

	if err := ks.Set("openai", "test-key"); err != nil {
		t.Fatalf("failed to set key: %v", err)
	}

	if got := ks.Get("openai"); got != "test-key" {
		t.Fatalf("expected test-key, got %q", got)
	}

	if !ks.Has("openai") {
		t.Fatal("expected Has(openai) true")
	}

	if err := ks.Set("openai", ""); err != nil {
		t.Fatalf("failed to delete key: %v", err)
	}

	if ks.Has("openai") {
		t.Fatal("expected Has(openai) false after deletion")
	}
}

func TestGetContextLimit(t *testing.T) {
	cases := map[string]int{
		"gpt-5.6-luna":    1_050_000,
		"gpt-5.6-sol":     1_050_000,
		"claude-opus-5":   1_000_000,
		"claude-opus-4-8": 1_000_000,
		"deepseek-chat":   1_000_000,
		"deepseek-v4-pro": 1_000_000,
		"phi4-mini":       128_000,
		"unknown-model":   DefaultContextLimit,
		"":                DefaultContextLimit,
	}

	for model, want := range cases {
		if got := GetContextLimit(model); got != want {
			t.Errorf("GetContextLimit(%q) = %d, want %d", model, got, want)
		}
	}
}

func TestCapabilitiesFor(t *testing.T) {
	caps := CapabilitiesFor("ollama", "phi4-mini")

	if !caps.Local {
		t.Error("expected local capability for ollama")
	}

	if caps.Remote {
		t.Error("expected remote=false for ollama")
	}

	openaiCaps := CapabilitiesFor("openai", "gpt-4o")

	if !openaiCaps.Embeddings {
		t.Error("expected embeddings capability for openai")
	}
}

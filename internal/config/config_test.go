// internal/config/config_test.go
// Purpose: Config loading edge cases for the hands-free path — an empty
// wake_word section in the file must not clobber the defaults, and a custom
// phrase must survive a reload.
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) *Config {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	cfg.ConfigPath = path
	return cfg
}

// TestWakeWordDefaultsAppliedWhenEmpty proves the fix: a file with an empty
// wake_word section (as written by older Helix versions) yields the safe
// defaults instead of a broken ""-phrase detector.
func TestWakeWordDefaultsAppliedWhenEmpty(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), `{
  "speech": {
    "stt": {"provider": "openai"},
    "tts": {"provider": "openai"},
    "wake_word": {}
  }
}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}

	ww := cfg.Speech.WakeWord
	if ww.Phrase != "hey helix" {
		t.Errorf("phrase = %q, want default \"hey helix\"", ww.Phrase)
	}
	if ww.Engine != "energy" {
		t.Errorf("engine = %q, want default \"energy\"", ww.Engine)
	}
	if ww.SensitivityPreset != "balanced" {
		t.Errorf("preset = %q, want default \"balanced\"", ww.SensitivityPreset)
	}
	if ww.CooldownS <= 0 || ww.ChunkMs <= 0 {
		t.Errorf("cooldown/chunk must be positive: %+v", ww)
	}
	if ww.Enabled {
		t.Error("wake word must stay opt-in (off) by default")
	}
}

// TestWakeWordCustomPhraseSurvives proves a user-set phrase is preserved
// across a reload (defaults only fill EMPTY fields).
func TestWakeWordCustomPhraseSurvives(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), `{
  "speech": {
    "wake_word": {"enabled": true, "phrase": "computer", "engine": "sidecar", "sidecar_url": "http://127.0.0.1:9090"}
  }
}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	ww := cfg.Speech.WakeWord
	if !ww.Enabled {
		t.Error("enabled flag lost on reload")
	}
	if ww.Phrase != "computer" {
		t.Errorf("phrase = %q, want preserved \"computer\"", ww.Phrase)
	}
	if ww.Engine != "sidecar" {
		t.Errorf("engine = %q, want preserved \"sidecar\"", ww.Engine)
	}
	if ww.SidecarURL != "http://127.0.0.1:9090" {
		t.Errorf("sidecar_url = %q, want preserved", ww.SidecarURL)
	}
}

func TestWakeWordDefaults(t *testing.T) {
	d := WakeWordDefaults()
	if d.Phrase == "" || d.Engine == "" || d.SensitivityPreset == "" {
		t.Fatalf("defaults must be complete: %+v", d)
	}
	if d.Enabled {
		t.Fatal("wake word must default to disabled (opt-in)")
	}
}

// --- BlackBox Phase 11: LLM resilience config -------------------------------

// TestLLMFallbackDefaultsWhenSectionAbsent proves an existing config written by
// an older Helix (no `llm` key at all) still arms the offline brain.
func TestLLMFallbackDefaultsWhenSectionAbsent(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), `{"provider": "openai"}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	f := cfg.LLM.Fallback
	if !f.FallbackEnabled() {
		t.Error("an absent llm section must default to fallback armed")
	}
	if f.Provider != "ollama" {
		t.Errorf("provider = %q, want the default \"ollama\"", f.Provider)
	}
	if f.EnsureReady {
		t.Error("ensure_ready must default to false — a model pull is a consent-gated download")
	}
}

// TestLLMFallbackExplicitDisableIsHonored is the reason Enabled is a *bool: with
// a plain bool, "absent" and "explicitly false" are the same zero value and the
// user could never turn a default-on feature off.
func TestLLMFallbackExplicitDisableIsHonored(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), `{"llm": {"fallback": {"enabled": false}}}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if cfg.LLM.Fallback.FallbackEnabled() {
		t.Fatal("an explicit \"enabled\": false must disable the fallback")
	}
	if cfg.AIFallback().Enabled {
		t.Fatal("the converted ai.LocalFallback must also report disabled")
	}
}

// TestLLMPartialSectionKeepsDefaults mirrors the wake-word merge discipline: a
// partial section layers over the defaults instead of replacing them.
func TestLLMPartialSectionKeepsDefaults(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), `{
  "llm": {
    "llamacpp_url": "http://127.0.0.1:9999/v1",
    "fallback": {"provider": "llamacpp", "model": "local-gguf"}
  }
}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if cfg.LLM.LlamaCppURL != "http://127.0.0.1:9999/v1" {
		t.Errorf("llamacpp_url = %q, want preserved", cfg.LLM.LlamaCppURL)
	}
	f := cfg.AIFallback()
	if !f.Enabled {
		t.Error("an unspecified enabled flag must keep the default (armed)")
	}
	if f.Provider != "llamacpp" || f.Model != "local-gguf" {
		t.Errorf("provider/model = %q/%q, want preserved", f.Provider, f.Model)
	}
	// Unset numerics must fall back to the package defaults, not to zero —
	// a zero threshold would trip the breaker on the first blip.
	if f.RetryAfter <= 0 {
		t.Errorf("retry_after must default, got %v", f.RetryAfter)
	}
}

func TestLLMDefaults(t *testing.T) {
	d := LLMDefaults()
	if !d.Fallback.FallbackEnabled() {
		t.Fatal("fallback must default to armed")
	}
	if d.Fallback.Provider == "" || d.Fallback.Threshold <= 0 || d.Fallback.RetryAfterS <= 0 {
		t.Fatalf("defaults must be complete: %+v", d.Fallback)
	}
}

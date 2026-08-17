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

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
	// This asserted "wake word must stay opt-in (off) by default" and PASSED
	// while the reversed default was broken — which makes it the test that
	// should have caught the bug and instead concealed it. It was reading a
	// plain bool that the load path never filled from the defaults, so it was
	// agreeing with the old intention about a value the new code believed it
	// had changed. A passing test that describes an intention nobody holds any
	// more is worse than a missing one.
	if !ww.Listening() || !ww.PromptArmed() {
		t.Error("an empty wake_word section must inherit the ON default, at the prompt and " +
			"between turns — read through the accessors, because the pointers are what " +
			"distinguish absent from explicitly false")
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
	if !ww.Listening() {
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

// The wake word is ON by default, reversing the strict opt-in this shipped
// with — an owner decision of 2026-09-09, after the two-switch opt-in confused
// a real user twice in one session.
//
// This test used to assert the opposite, and is REWRITTEN rather than deleted
// because the guarantees that survive are the load-bearing ones. Defaulting on
// means a fresh install listens at an idle prompt; what keeps that honest is
// not the default but the three properties below, and those are what a future
// change must not quietly drop.
func TestWakeWordDefaults(t *testing.T) {
	d := WakeWordDefaults()
	if d.Phrase == "" || d.Engine == "" || d.SensitivityPreset == "" {
		t.Fatalf("defaults must be complete: %+v", d)
	}
	if !d.Listening() {
		t.Error("the wake word is on by default now — a default nobody turns on was not " +
			"\"Helix alive and my keyboard at the same time\"")
	}
	if !d.PromptArmed() {
		t.Error("and it listens at the PROMPT by default, which is the half that makes it " +
			"one feature instead of two switches")
	}
	// The engine matters to the default in a way worth pinning: `energy` needs
	// no sidecar, so a default-on wake word works on a fresh install. Defaulting
	// to `sidecar` would ship a feature that is on and cannot run.
	if d.Engine != "energy" {
		t.Errorf("default engine = %q — a default-ON wake word must use the engine that "+
			"needs no sidecar, or it is enabled and broken out of the box", d.Engine)
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

// TestWakeListeningSurvivesTheLoadPath is the test that should have existed
// when the default was reversed, and did not.
//
// The default was changed to true and had NO EFFECT on any real session:
// applyWakeWordDefaults fills empty strings and zero ints and never touched the
// booleans, so `Enabled` came from the file or from the zero value.
// TestWakeWordDefaults asserted the DEFAULTS FUNCTION returned true and passed
// the whole time — a test of a mechanism, not a behaviour (§9 rule 8). The
// owner's config said `"enabled": true` with no `always_listen`, the prompt
// never armed, and talking to it did nothing.
//
// So this asserts the config a user actually ends up with, through
// LoadPreferences, for the four cases that differ.
func TestWakeListeningSurvivesTheLoadPath(t *testing.T) {
	cases := []struct {
		name          string
		wake          string
		wantListening bool
		wantArmed     bool
	}{{
		// The case that broke: enabled explicitly, always_listen absent.
		name:          "enabled with always_listen absent",
		wake:          `{"enabled": true}`,
		wantListening: true,
		wantArmed:     true,
	}, {
		// A config from before this feature existed at all.
		name:          "empty section",
		wake:          `{}`,
		wantListening: true,
		wantArmed:     true,
	}, {
		// Explicit OFF must stay off. With a plain bool this was
		// indistinguishable from absent, so the feature could not be disabled.
		name:          "explicitly disabled",
		wake:          `{"enabled": false}`,
		wantListening: false,
		wantArmed:     false,
	}, {
		// The owner's file, verbatim: enabled by an earlier `wake on`, every
		// tunable present, no always_listen key. The tunables travel through
		// applyWakeWordDefaults, so a future version of that function that
		// resets the booleans when the section is non-empty fails here.
		name: "owner's config, every tunable set, no always_listen",
		wake: `{"enabled": true, "engine": "energy", "phrase": "hey helix",
                "sensitivity_preset": "balanced", "cooldown_s": 2, "chunk_ms": 1500}`,
		wantListening: true,
		wantArmed:     true,
	}, {
		// The narrow behaviour: listen between turns, leave the prompt alone.
		// This is the escape hatch that replaces a second command.
		name:          "listening but prompt not armed",
		wake:          `{"enabled": true, "always_listen": false}`,
		wantListening: true,
		wantArmed:     false,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := writeConfig(t, t.TempDir(), `{
  "speech": {"stt": {"provider": "openai"}, "tts": {"provider": "openai"},
             "wake_word": `+tc.wake+`}
}`)
			if err := cfg.LoadPreferences(); err != nil {
				t.Fatalf("LoadPreferences: %v", err)
			}
			ww := cfg.Speech.WakeWord
			if got := ww.Listening(); got != tc.wantListening {
				t.Errorf("Listening() = %v, want %v — this is the value the session uses, "+
					"not the one WakeWordDefaults returns", got, tc.wantListening)
			}
			if got := ww.PromptArmed(); got != tc.wantArmed {
				t.Errorf("PromptArmed() = %v, want %v", got, tc.wantArmed)
			}
		})
	}
}

// An armed prompt requires listening. Arming without the wake word would be a
// microphone held open for a detector nobody asked to run.
func TestPromptArmedImpliesListening(t *testing.T) {
	off := SpeechWakeConfig{Enabled: BoolPtr(false), AlwaysListen: BoolPtr(true)}
	if off.PromptArmed() {
		t.Error("the prompt is armed while listening is off")
	}
}

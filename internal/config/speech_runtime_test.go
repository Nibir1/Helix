// internal/config/speech_runtime_test.go
// Purpose: pin the config→speech conversion, and specifically that per-provider
// Endpoints survive it.
//
// This is the third time Endpoints has been dropped at a struct boundary. The
// first cost a wizard run (the commit step rebuilt the struct without them);
// the second was the daemon converting the section inline and omitting the
// field, so `helix` reached a relocated sidecar and `helix daemon` silently did
// not. There is now one conversion, and this test is what keeps it that way.
package config

import "testing"

func TestSpeechRuntimeCarriesEndpoints(t *testing.T) {
	sc := SpeechConfig{
		STT: SpeechSTTConfig{
			Provider:      "groq",
			Model:         "whisper-large-v3-turbo",
			Fallbacks:     []string{"whisper-local"},
			Endpoints:     map[string]string{"whisper-local": "http://127.0.0.1:28861"},
			StreamChunkMs: 300,
		},
		TTS: SpeechTTSConfig{
			Provider:    "openai",
			Model:       "gpt-4o-mini-tts",
			Voice:       "alloy",
			Fallbacks:   []string{"piper-local"},
			Endpoints:   map[string]string{"piper-local": "http://127.0.0.1:28183"},
			FirstByteMs: 800,
		},
	}

	rt := sc.Runtime()

	if got := rt.STT.Endpoints["whisper-local"]; got != "http://127.0.0.1:28861" {
		t.Fatalf("STT endpoint override lost in conversion: %q", got)
	}
	if got := rt.TTS.Endpoints["piper-local"]; got != "http://127.0.0.1:28183" {
		t.Fatalf("TTS endpoint override lost in conversion: %q", got)
	}
}

// Every field the persisted section carries must reach the runtime config. A
// field added to one struct and forgotten in the conversion is exactly how the
// Endpoints bug happened, so this asserts the whole payload rather than only
// the field that broke.
func TestSpeechRuntimeCarriesEveryField(t *testing.T) {
	sc := SpeechConfig{
		STT: SpeechSTTConfig{
			Provider: "deepgram", Model: "nova-3", BaseURL: "http://stt.local",
			Fallbacks: []string{"whisper-local"}, StreamChunkMs: 250,
		},
		TTS: SpeechTTSConfig{
			Provider: "deepgram", Model: "aura-2-thalia-en", Voice: "aura-2-thalia-en",
			BaseURL: "http://tts.local", Fallbacks: []string{"piper-local"}, FirstByteMs: 400,
		},
	}
	rt := sc.Runtime()

	if rt.STT.Provider != "deepgram" || rt.STT.Model != "nova-3" ||
		rt.STT.BaseURL != "http://stt.local" || rt.STT.StreamChunkMs != 250 ||
		len(rt.STT.Fallbacks) != 1 || rt.STT.Fallbacks[0] != "whisper-local" {
		t.Fatalf("STT conversion incomplete: %+v", rt.STT)
	}
	if rt.TTS.Provider != "deepgram" || rt.TTS.Model != "aura-2-thalia-en" ||
		rt.TTS.Voice != "aura-2-thalia-en" || rt.TTS.BaseURL != "http://tts.local" ||
		rt.TTS.FirstByteMs != 400 ||
		len(rt.TTS.Fallbacks) != 1 || rt.TTS.Fallbacks[0] != "piper-local" {
		t.Fatalf("TTS conversion incomplete: %+v", rt.TTS)
	}
}

// The voice log is off unless the user says otherwise, and an absent section
// must mean off rather than "whatever the zero value happens to be later".
func TestVoiceLogDefaultsOff(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(), `{"user_preferences":{}}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if cfg.VoiceLog.Enabled {
		t.Fatal("voice log must be disabled when the section is absent")
	}
}

func TestVoiceLogExplicitEnableIsHonored(t *testing.T) {
	cfg := writeConfig(t, t.TempDir(),
		`{"voice_log":{"enabled":true,"max_bytes":2048,"keep_files":2}}`)
	if err := cfg.LoadPreferences(); err != nil {
		t.Fatalf("LoadPreferences: %v", err)
	}
	if !cfg.VoiceLog.Enabled {
		t.Fatal("explicit voice_log.enabled=true must be honored")
	}
	if cfg.VoiceLog.MaxBytes != 2048 || cfg.VoiceLog.KeepFiles != 2 {
		t.Fatalf("rotation settings lost: %+v", cfg.VoiceLog)
	}
}

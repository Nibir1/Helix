// internal/config/config.go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"helix/internal/ai"
	"helix/internal/commands"
)

// Config holds runtime configuration and paths for Helix.
type Config struct {
	ModelDir              string                 `json:"model_dir"`
	ModelFile             string                 `json:"model_file"`
	HistoryPath           string                 `json:"history_path"`
	ConfigPath            string                 `json:"config_path"`
	OpenAIKeyPath         string                 `json:"openai_key_path"`
	Provider              string                 `json:"provider"`
	ProviderModel         string                 `json:"provider_model"`
	CustomProviderBaseURL string                 `json:"custom_provider_base_url"`
	UserPrefs             UserPrefs              `json:"user_preferences"`
	Speech                SpeechConfig           `json:"speech"`
	Daemon                DaemonConfig           `json:"daemon"`
	Vision                VisionConfig           `json:"vision"`
	Ambient               AmbientConfig          `json:"ambient"`
	ModelConfig           ai.ModelConfig         `json:"model_config"`
	ExecuteConfig         commands.ExecuteConfig `json:"execute_config"`
}

// UserPrefs holds user preferences.
type UserPrefs struct {
	AutoConfirm  bool   `json:"auto_confirm"`
	ColorMode    string `json:"color_mode"`
	TypingEffect bool   `json:"typing_effect"`
	TypewriteAll bool   `json:"typewrite_all"`
	DefaultMode  string `json:"default_mode"`
	SafeMode     bool   `json:"safe_mode"`
	UserName     string `json:"user_name"`
	DebugMode    bool   `json:"debug_mode"`
	VoiceMode    bool   `json:"voice_mode"` // BlackBox Phase 2: default input channel
}

// SpeechSTTConfig selects the speech-to-text provider chain (BlackBox §7).
type SpeechSTTConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	BaseURL   string   `json:"base_url"`
	Fallbacks []string `json:"fallbacks"`
	// StreamChunkMs is the streaming-STT capture chunk length (0 → 300ms).
	StreamChunkMs int `json:"stream_chunk_ms"`
}

// SpeechTTSConfig selects the text-to-speech provider chain.
type SpeechTTSConfig struct {
	Provider  string   `json:"provider"`
	Model     string   `json:"model"`
	Voice     string   `json:"voice"`
	BaseURL   string   `json:"base_url"`
	Fallbacks []string `json:"fallbacks"`
	// FirstByteMs is the TTS first-byte latency budget (0 → 800ms).
	FirstByteMs int `json:"first_byte_ms"`
}

// SpeechWakeConfig controls wake-word listening (BlackBox §7).
type SpeechWakeConfig struct {
	Enabled           bool   `json:"enabled"`
	Engine            string `json:"engine"`             // "energy" (default) | "sidecar"
	SidecarURL        string `json:"sidecar_url"`        // sidecar engine endpoint
	Phrase            string `json:"phrase"`             // default "hey helix"
	SensitivityPreset string `json:"sensitivity_preset"` // strict | balanced | loose
	CooldownS         int    `json:"cooldown_s"`
	ChunkMs           int    `json:"chunk_ms"` // scanner chunk length
}

// SpeechConfig is the speech subsystem section of ~/.helix/config.json.
type SpeechConfig struct {
	STT      SpeechSTTConfig  `json:"stt"`
	TTS      SpeechTTSConfig  `json:"tts"`
	WakeWord SpeechWakeConfig `json:"wake_word"`
}

// DaemonConfig controls the BlackBox Phase 4 Living AI service (§7). The
// interaction journal is always on in v1 (safe and /purge-able); Autostart is
// honored by `helix daemon install` (RunAtLoad / KeepAlive / Restart=on-failure).
type DaemonConfig struct {
	Autostart        bool `json:"autostart"`          // service auto-start hint
	SessionTurns     int  `json:"session_turns"`      // conversation ring-buffer size (0 → default)
	BreakReminderMin int  `json:"break_reminder_min"` // focus-break reminder cadence; 0 = off (default)
}

// VisionConfig controls BlackBox Phase 5 opt-in camera perception (§7).
// Enabled defaults to false — strict opt-in is a privacy guarantee.
type VisionConfig struct {
	Enabled          bool   `json:"enabled"`             // master opt-in switch
	Provider         string `json:"provider"`            // dedicated vision LLM ("" → active chat provider)
	MaxFramesPerTurn int    `json:"max_frames_per_turn"` // default 1
}

// AmbientConfig controls BlackBox Phase 6 (optional) auditory awareness (§7).
// Disabled by default; ResponseMode is "vocal" | "log" | "ignore".
type AmbientConfig struct {
	Enabled      bool            `json:"enabled"`
	Sensitivity  float64         `json:"sensitivity"` // 0..1; 0 → package default (0.5)
	ResponseMode string          `json:"response_mode"`
	Categories   map[string]bool `json:"categories"` // e.g. {"loud_noise": true}
}

// DefaultConfig returns sane default paths for Helix.
func DefaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	modelDir := os.Getenv("HELIX_MODEL_DIR")
	if modelDir == "" {
		modelDir = filepath.Join(home, ".helix", "models")
	}
	configDir := filepath.Join(home, ".helix")
	openAIKeyPath := filepath.Join(configDir, "openai_api_key")
	modelFile := filepath.Join(modelDir, "gemma-4-e2b-it-Q4_K_M.gguf")
	cfg := &Config{
		ModelDir:              modelDir,
		ModelFile:             modelFile,
		HistoryPath:           filepath.Join(home, ".helix_history"),
		ConfigPath:            filepath.Join(configDir, "config.json"),
		OpenAIKeyPath:         openAIKeyPath,
		Provider:              "",
		ProviderModel:         "",
		CustomProviderBaseURL: "",
		UserPrefs: UserPrefs{
			AutoConfirm:  false,
			ColorMode:    "auto",
			TypingEffect: true,
			TypewriteAll: false, // Phase 15: Default to off (AI only)
			DefaultMode:  "ask",
			SafeMode:     true,
			UserName:     "",
			DebugMode:    false,
		},
		ModelConfig:   ai.DefaultModelConfig(),
		ExecuteConfig: commands.DefaultExecuteConfig(),
	}
	_ = cfg.LoadPreferences()
	return cfg, nil
}

// EnsureModelDir ensures that the model directory exists.
func (cfg *Config) EnsureModelDir() error {
	return os.MkdirAll(cfg.ModelDir, 0o755)
}

// EnsureConfigDir ensures that the config directory exists.
func (cfg *Config) EnsureConfigDir() error {
	return os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755)
}

// LoadPreferences loads user preferences from config file.
func (cfg *Config) LoadPreferences() error {
	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		return nil
	}
	var prefs Config
	if err := json.Unmarshal(data, &prefs); err != nil {
		return fmt.Errorf("error parsing config file: %w", err)
	}
	cfg.UserPrefs = prefs.UserPrefs
	if prefs.ModelConfig.MaxTokens > 0 {
		cfg.ModelConfig = prefs.ModelConfig
	}
	if prefs.Provider != "" {
		cfg.Provider = prefs.Provider
	}
	if prefs.ProviderModel != "" {
		cfg.ProviderModel = prefs.ProviderModel
	}
	if prefs.CustomProviderBaseURL != "" {
		cfg.CustomProviderBaseURL = prefs.CustomProviderBaseURL
	}
	if prefs.Speech.STT.Provider != "" || prefs.Speech.TTS.Provider != "" {
		cfg.Speech = prefs.Speech
	}
	// Wake word is ALWAYS field-wise merged (not gated on STT/TTS providers):
	// a config that sets only the wake_word section must still load, and
	// empty values in the file must never wipe the defaults — otherwise
	// enabling hands-free would silently produce a broken ""-phrase detector.
	mergeWakeWord(&cfg.Speech.WakeWord, prefs.Speech.WakeWord)
	applyWakeWordDefaults(&cfg.Speech.WakeWord)
	// Field-wise so unset daemon keys never clobber defaults.
	if prefs.Daemon.SessionTurns > 0 {
		cfg.Daemon.SessionTurns = prefs.Daemon.SessionTurns
	}
	if prefs.Daemon.BreakReminderMin > 0 {
		cfg.Daemon.BreakReminderMin = prefs.Daemon.BreakReminderMin
	}
	cfg.Daemon.Autostart = prefs.Daemon.Autostart
	cfg.Vision.Enabled = prefs.Vision.Enabled
	if prefs.Vision.Provider != "" {
		cfg.Vision.Provider = prefs.Vision.Provider
	}
	if prefs.Vision.MaxFramesPerTurn > 0 {
		cfg.Vision.MaxFramesPerTurn = prefs.Vision.MaxFramesPerTurn
	}
	// Ambient's zero value is the correct default (disabled); copy wholesale.
	cfg.Ambient = prefs.Ambient
	return nil
}

// WakeWordDefaults are the safe, everywhere-works defaults for hands-free
// detection (ADR-002 honesty: energy onset is the default engine).
func WakeWordDefaults() SpeechWakeConfig {
	return SpeechWakeConfig{
		Enabled:           false, // strict opt-in (privacy)
		Engine:            "energy",
		Phrase:            "hey helix",
		SensitivityPreset: "balanced",
		CooldownS:         2,
		ChunkMs:           1500,
	}
}

// mergeWakeWord copies non-empty wake-word fields from src into dst so a
// partial section in the file layers over the defaults instead of replacing
// them. Enabled only flips to true from the file (default is off, so an
// explicit disable needs no special case).
func mergeWakeWord(dst *SpeechWakeConfig, src SpeechWakeConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Engine != "" {
		dst.Engine = src.Engine
	}
	if src.Phrase != "" {
		dst.Phrase = src.Phrase
	}
	if src.SensitivityPreset != "" {
		dst.SensitivityPreset = src.SensitivityPreset
	}
	if src.SidecarURL != "" {
		dst.SidecarURL = src.SidecarURL
	}
	if src.CooldownS > 0 {
		dst.CooldownS = src.CooldownS
	}
	if src.ChunkMs > 0 {
		dst.ChunkMs = src.ChunkMs
	}
}

// applyWakeWordDefaults fills empty wake-word fields with the defaults.
func applyWakeWordDefaults(w *SpeechWakeConfig) {
	def := WakeWordDefaults()
	if w.Engine == "" {
		w.Engine = def.Engine
	}
	if w.Phrase == "" {
		w.Phrase = def.Phrase
	}
	if w.SensitivityPreset == "" {
		w.SensitivityPreset = def.SensitivityPreset
	}
	if w.CooldownS <= 0 {
		w.CooldownS = def.CooldownS
	}
	if w.ChunkMs <= 0 {
		w.ChunkMs = def.ChunkMs
	}
}

// SavePreferences saves user preferences to config file.
func (cfg *Config) SavePreferences() error {
	if err := cfg.EnsureConfigDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cfg.ConfigPath, data, 0o644)
}

// Versioning and model metadata.
const (
	HelixVersion  = "1.0.0"
	ModelName     = "TinyLlama-1.1B-Chat-v1.0-GGUF"
	ModelURL      = "https://huggingface.co/TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF/resolve/main/tinyllama-1.1b-chat-v1.0.Q4_0.gguf"
	ModelChecksum = "da3087fb14aede55fde6eb81a0e55e886810e43509ec82ecdc7aa5d62a03b556"
)

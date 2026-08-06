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
	ModelConfig           ai.ModelConfig         `json:"model_config"`
	ExecuteConfig         commands.ExecuteConfig `json:"execute_config"`
}

// UserPrefs holds user preferences.
type UserPrefs struct {
	AutoConfirm  bool   `json:"auto_confirm"`
	ColorMode    string `json:"color_mode"`
	TypingEffect bool   `json:"typing_effect"`
	DefaultMode  string `json:"default_mode"`
	SafeMode     bool   `json:"safe_mode"`
	UserName     string `json:"user_name"`
	DebugMode    bool   `json:"debug_mode"`
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
	modelFile := filepath.Join(modelDir, "tinyllama-1.1b-chat-v1.0.Q4_0.gguf")
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
			DefaultMode:  "ask",
			SafeMode:     true,
			UserName:     "", // Defaults to Nahasat Nibir in prompt.go if empty
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
	return nil
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

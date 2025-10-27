package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"helix/internal/ai"
	"helix/internal/commands"
)

// Config holds runtime configuration and paths for Helix
type Config struct {
	ModelDir      string                 `json:"model_dir"`
	ModelFile     string                 `json:"model_file"`
	HistoryPath   string                 `json:"history_path"`
	ConfigPath    string                 `json:"config_path"`
	UserPrefs     UserPrefs              `json:"user_preferences"`
	ModelConfig   ai.ModelConfig         `json:"model_config"`
	ExecuteConfig commands.ExecuteConfig `json:"execute_config"`
}

// UserPrefs holds user preferences
type UserPrefs struct {
	AutoConfirm  bool   `json:"auto_confirm"`
	ColorMode    string `json:"color_mode"`
	TypingEffect bool   `json:"typing_effect"`
	DefaultMode  string `json:"default_mode"` // "ask" or "cmd"
	SafeMode     bool   `json:"safe_mode"`
}

// DefaultConfig returns sane default paths for Helix
func DefaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Allow override via environment variable
	modelDir := os.Getenv("HELIX_MODEL_DIR")
	if modelDir == "" {
		modelDir = filepath.Join(home, ".helix", "models")
	}

	configDir := filepath.Join(home, ".helix")
	modelFile := filepath.Join(modelDir, "llama-2-7b-chat.Q4_0.gguf")

	cfg := &Config{
		ModelDir:    modelDir,
		ModelFile:   modelFile,
		HistoryPath: filepath.Join(home, ".helix_history"),
		ConfigPath:  filepath.Join(configDir, "config.json"),
		UserPrefs: UserPrefs{
			AutoConfirm:  false,
			ColorMode:    "auto",
			TypingEffect: true,
			DefaultMode:  "ask",
			SafeMode:     true,
		},
		ModelConfig:   ai.DefaultModelConfig(),
		ExecuteConfig: commands.DefaultExecuteConfig(),
	}

	// Load user preferences if config file exists
	cfg.LoadPreferences()

	return cfg, nil
}

// EnsureModelDir ensures that the model directory exists
func (cfg *Config) EnsureModelDir() error {
	return os.MkdirAll(cfg.ModelDir, 0755)
}

// EnsureConfigDir ensures that the config directory exists
func (cfg *Config) EnsureConfigDir() error {
	return os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0755)
}

// LoadPreferences loads user preferences from config file
func (cfg *Config) LoadPreferences() error {
	data, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		// Config file doesn't exist, use defaults
		return nil
	}

	var prefs Config
	err = json.Unmarshal(data, &prefs)
	if err != nil {
		return fmt.Errorf("error parsing config file: %w", err)
	}

	// Merge loaded preferences
	cfg.UserPrefs = prefs.UserPrefs
	if prefs.ModelConfig.MaxTokens > 0 {
		cfg.ModelConfig = prefs.ModelConfig
	}

	return nil
}

// SavePreferences saves user preferences to config file
func (cfg *Config) SavePreferences() error {
	if err := cfg.EnsureConfigDir(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(cfg.ConfigPath, data, 0644)
}

// Versioning and Model metadata
const (
	HelixVersion  = "0.3.0"
	ModelName     = "llama-2-7b-chat.gguf"
	ModelURL      = "https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF/resolve/main/llama-2-7b-chat.Q4_0.gguf"
	ModelChecksum = "9958ee9b670594147b750bbc7d0540b928fa12dcc5dd4c58cc56ed2eb85e371b"
)

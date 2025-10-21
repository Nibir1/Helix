package main

import (
	"os"
	"path/filepath"
)

// Config holds runtime configuration and paths for Helix.
type Config struct {
	ModelDir    string // Directory containing AI models
	ModelFile   string // Path to the active model
	HistoryPath string // Command history file
}

// DefaultConfig returns sane default paths for Helix.
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
	modelFile := filepath.Join(modelDir, "llama3.2.gguf")

	cfg := &Config{
		ModelDir:    modelDir,
		ModelFile:   modelFile,
		HistoryPath: filepath.Join(home, ".helix_history"),
	}
	return cfg, nil
}

// EnsureModelDir ensures that the model directory exists.
func (cfg *Config) EnsureModelDir() error {
	return os.MkdirAll(cfg.ModelDir, 0755)
}

// Versioning and Model metadata
const (
	HelixVersion  = "0.1.0"
	ModelName     = "llama-2-7b-chat.gguf"
	ModelURL      = "https://huggingface.co/TheBloke/Llama-2-7B-Chat-GGUF/resolve/main/llama-2-7b-chat.Q4_0.gguf"
	ModelChecksum = "9958ee9b670594147b750bbc7d0540b928fa12dcc5dd4c58cc56ed2eb85e371b"
)

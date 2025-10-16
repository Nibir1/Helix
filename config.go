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
	ModelName     = "Llama-3.2-3B-Instruct-Q4_K_M"
	ModelURL      = "https://huggingface.co/hugging-quants/Llama-3.2-3B-Instruct-Q4_K_M-GGUF/resolve/main/llama-3.2-3b-instruct-q4_k_m.gguf"
	ModelChecksum = "c55a83bfb6396799337853ca69918a0b9bbb2917621078c34570bc17d20fd7a1"
)

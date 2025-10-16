package main

import (
	"os"
	"path/filepath"
)

// Config holds runtime configuration and paths for Helix.
type Config struct {
	ModelPath   string
	HistoryPath string
}

// DefaultConfig returns a sane default configuration.
func DefaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		ModelPath:   filepath.Join(home, ".helix", "model"),
		HistoryPath: filepath.Join(home, ".helix_history"),
	}
	return cfg, nil
}

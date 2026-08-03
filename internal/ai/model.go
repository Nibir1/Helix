// internal/ai/model.go
// Purpose: Provider-backed model execution facade.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helix/internal/providers"
)

// ModelConfig holds inference parameters.
type ModelConfig struct {
	Temperature float32
	TopP        float32
	TopK        int
	MaxTokens   int
}

// DefaultModelConfig returns safe general chat settings.
func DefaultModelConfig() ModelConfig {
	return ModelConfig{
		Temperature: 0.7,
		TopP:        0.9,
		TopK:        40,
		MaxTokens:   512,
	}
}

// PlannerModelConfig returns strict JSON planner settings.
func PlannerModelConfig() ModelConfig {
	return ModelConfig{
		Temperature: 0.2,
		TopP:        0.95,
		TopK:        40,
		MaxTokens:   2048,
	}
}

// RunModel runs a general prompt.
func RunModel(prompt string) (string, error) {
	return RunModelWithConfig(prompt, DefaultModelConfig())
}

// RunPlannerModel runs a planner prompt.
func RunPlannerModel(prompt string) (string, error) {
	return RunPlannerWithRetry(prompt)
}

// RunModelWithConfig runs the active provider with explicit settings.
func RunModelWithConfig(prompt string, config ModelConfig) (string, error) {
	prompt = strings.TrimSpace(prompt)

	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	if activeProvider == nil {
		return "", fmt.Errorf("no AI provider configured")
	}

	if config.MaxTokens <= 0 {
		config.MaxTokens = DefaultModelConfig().MaxTokens
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	temp := float64(config.Temperature)

	req := providers.ChatRequest{
		Model:       activeModel,
		Messages:    []providers.ChatMessage{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   config.MaxTokens,
	}

	out, err := providers.CollectChat(ctx, activeProvider, req)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// LoadModel is retained for compatibility and starts a llama.cpp server.
func LoadModel(modelPath string) error {
	return EnsureLlamaCppServer(context.Background(), modelPath)
}

// CloseModel stops local runtimes.
func CloseModel() {
	StopLocalRuntimes()
}

// ModelIsLoaded reports whether a provider is active.
func ModelIsLoaded() bool {
	return activeProvider != nil
}

// TestModelWithSimplePrompt performs a basic smoke test.
func TestModelWithSimplePrompt() (string, error) {
	return RunModel("Say 'Hello world' in one short sentence.")
}

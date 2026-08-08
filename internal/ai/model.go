// internal/ai/model.go
// Purpose: Provider-backed model execution facade.
// Hardening: planner/chat HTTP waits register their cancel func with the
// interrupt manager so Ctrl+C aborts AI latency instead of killing Helix.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helix/internal/providers"
	"helix/internal/utils"
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
//
// Args:
//   - prompt: user/system prompt text.
//   - config: inference parameters.
//
// Returns: trimmed model output or error (context.Canceled on Ctrl+C).
// Complexity: O(1) HTTP round trip(s).
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
	// FIX (interrupt hardening): Ctrl+C while waiting on the neural link
	// cancels the request and returns control to the prompt.
	unreg := utils.RegisterOperation(cancel)
	defer unreg()
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

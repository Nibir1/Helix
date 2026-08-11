// internal/ai/model.go
//
// Purpose: Provider-backed model execution facade.
//
// Hardening:
//   - planner/chat HTTP waits register their cancel func with the interrupt
//     manager so Ctrl+C aborts AI latency instead of killing Helix.
//   - planner and chat calls now use explicit shorter timeouts so a slow or
//     unreachable provider cannot stall the interactive shell for minutes.
package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"helix/internal/providers"
	"helix/internal/utils"
)

// Timeout policy.
//
// Planner calls are interactive and should fail fast. Chat calls may be a bit
// longer, but still must not hang the shell for minutes.
const (
	// DefaultChatTimeout bounds normal chat/model calls.
	DefaultChatTimeout = 90 * time.Second

	// PlannerTimeout bounds strict-JSON planner calls.
	PlannerTimeout = 60 * time.Second
)

// ModelConfig holds inference parameters.
type ModelConfig struct {
	Temperature float32
	TopP        float32
	TopK        int
	MaxTokens   int
}

// DefaultModelConfig returns safe general chat settings.
//
// Args: none.
// Returns: ModelConfig.
// Complexity: O(1).
func DefaultModelConfig() ModelConfig {
	return ModelConfig{
		Temperature: 0.7,
		TopP:        0.9,
		TopK:        40,
		MaxTokens:   512,
	}
}

// PlannerModelConfig returns strict JSON planner settings.
//
// Args: none.
// Returns: ModelConfig.
// Complexity: O(1).
func PlannerModelConfig() ModelConfig {
	return ModelConfig{
		Temperature: 0.2,
		TopP:        0.95,
		TopK:        40,
		MaxTokens:   2048,
	}
}

// RunModel runs a general prompt with the default chat timeout.
//
// Args:
//   - prompt: user prompt text.
//
// Returns:
//   - trimmed model output or error.
//
// Complexity: O(1) provider round trip.
func RunModel(prompt string) (string, error) {
	return RunModelWithTimeout(prompt, DefaultModelConfig(), DefaultChatTimeout)
}

// RunPlannerModel runs a planner prompt through the retry adapter.
//
// Args:
//   - prompt: planner prompt.
//
// Returns:
//   - raw planner output or error.
//
// Complexity: O(1) provider round trip(s).
func RunPlannerModel(prompt string) (string, error) {
	return RunPlannerWithRetry(prompt)
}

// RunModelWithConfig runs the active provider with explicit settings and the
// default chat timeout.
//
// Args:
//   - prompt: user/system prompt text.
//   - config: inference parameters.
//
// Returns:
//   - trimmed model output or error.
//
// Complexity: O(1) provider round trip.
func RunModelWithConfig(prompt string, config ModelConfig) (string, error) {
	return RunModelWithTimeout(prompt, config, DefaultChatTimeout)
}

// RunModelWithTimeout runs the active provider with explicit settings and an
// explicit timeout.
//
// Args:
//   - prompt: user/system prompt text.
//   - config: inference parameters.
//   - timeout: maximum time to wait for the provider.
//
// Returns:
//   - trimmed model output or error.
//
// Complexity: O(1) provider round trip.
func RunModelWithTimeout(prompt string, config ModelConfig, timeout time.Duration) (string, error) {
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

	if timeout <= 0 {
		timeout = DefaultChatTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Ctrl+C while waiting on the neural link cancels the request and returns
	// control to the prompt.
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

// ModelIsLoaded reports whether a provider is active.
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func ModelIsLoaded() bool {
	return activeProvider != nil
}

// TestModelWithSimplePrompt performs a basic smoke test.
//
// Args: none.
// Returns:
//   - model output or error.
//
// Complexity: O(1) provider round trip.
func TestModelWithSimplePrompt() (string, error) {
	return RunModel("Say 'Hello world' in one short sentence.")
}

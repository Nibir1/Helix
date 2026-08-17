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

	// P11.2: the breaker chooses the provider for this call — the configured
	// one normally, the local brain while degraded, or a half-open cloud probe
	// when the retry window has elapsed.
	provider, model, probing := resolveProvider()
	if provider == nil {
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
		Model:       model,
		Messages:    []providers.ChatMessage{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   config.MaxTokens,
	}

	out, err := providers.CollectChat(ctx, provider, req)
	noteCallResult(err, probing)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// StreamModel runs a prompt and delivers text to onChunk as it arrives,
// returning the complete response (BlackBox P8.8).
//
// Callers still receive the full text: downstream consumers — the fenced-script
// promoter, the session store — need the whole response, so streaming changes
// when bytes are DISPLAYED, not what the caller ends up holding.
//
// Like every other entry point here it resolves the provider through the P11.2
// breaker, registers its cancel func with the interrupt manager, and reports
// the outcome to the failover bookkeeping.
//
// Args:
//   - prompt: user prompt text.
//   - config: inference parameters.
//   - timeout: maximum time to wait for the provider.
//   - onChunk: receives each fragment; may be nil.
//
// Returns: the complete trimmed text, or an error (with whatever text arrived).
// Complexity: O(1) provider round trip, streamed.
func StreamModel(
	prompt string, config ModelConfig, timeout time.Duration, onChunk func(string),
) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	provider, model, probing := resolveProvider()
	if provider == nil {
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

	unreg := utils.RegisterOperation(cancel)
	defer unreg()

	temp := float64(config.Temperature)
	ch, err := provider.Chat(ctx, providers.ChatRequest{
		Model:       model,
		Messages:    []providers.ChatMessage{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   config.MaxTokens,
	})
	if err != nil {
		noteCallResult(err, probing)
		return "", err
	}

	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			// Ctrl+C or timeout: keep whatever was already rendered.
			noteCallResult(ctx.Err(), probing)
			return strings.TrimSpace(b.String()), ctx.Err()

		case chunk, ok := <-ch:
			if !ok {
				noteCallResult(nil, probing)
				return strings.TrimSpace(b.String()), nil
			}
			if chunk.Error != nil {
				noteCallResult(chunk.Error, probing)
				return strings.TrimSpace(b.String()), chunk.Error
			}
			if chunk.Content != "" {
				b.WriteString(chunk.Content)
				if onChunk != nil {
					onChunk(chunk.Content)
				}
			}
			if chunk.Done {
				noteCallResult(nil, probing)
				return strings.TrimSpace(b.String()), nil
			}
		}
	}
}

// RunToolCall runs a prompt with native tool calling and returns the model's
// structured response (BlackBox P8.7).
//
// It mirrors RunModelWithTimeout exactly — same breaker-resolved provider, same
// interrupt registration, same failover bookkeeping — differing only in that it
// attaches tool definitions and returns tool calls instead of discarding them.
//
// Args:
//   - prompt: user/system prompt text.
//   - tools: tool definitions offered to the model.
//   - choice: providers.ToolChoiceAuto or ToolChoiceRequired ("" → provider default).
//   - config: inference parameters.
//   - timeout: maximum time to wait for the provider.
//
// Returns: the assembled text + tool calls, or an error.
// Complexity: O(1) provider round trip.
func RunToolCall(
	prompt string, tools []providers.ToolDefinition, choice string,
	config ModelConfig, timeout time.Duration,
) (providers.ChatResult, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return providers.ChatResult{}, fmt.Errorf("empty prompt")
	}

	provider, model, probing := resolveProvider()
	if provider == nil {
		return providers.ChatResult{}, fmt.Errorf("no AI provider configured")
	}

	if config.MaxTokens <= 0 {
		config.MaxTokens = DefaultModelConfig().MaxTokens
	}
	if timeout <= 0 {
		timeout = DefaultChatTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	unreg := utils.RegisterOperation(cancel)
	defer unreg()

	temp := float64(config.Temperature)
	res, err := providers.CollectChatResult(ctx, provider, providers.ChatRequest{
		Model:       model,
		Messages:    []providers.ChatMessage{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   config.MaxTokens,
		Tools:       tools,
		ToolChoice:  choice,
	})
	noteCallResult(err, probing)
	if err != nil {
		return res, err
	}
	return res, nil
}

// ToolCallingAvailable reports whether the provider that would serve the next
// call supports native function calling.
//
// It asks about the provider the BREAKER would pick, not the configured one:
// while degraded to a local model, tool calling is unavailable even though the
// configured cloud provider supports it (P11.2 × P8.7).
func ToolCallingAvailable() bool {
	p, model, _ := resolveProvider()
	if p == nil {
		return false
	}
	if model == "" {
		model = p.DefaultModel()
	}
	return providers.SupportsToolUse(p.Name(), model)
}

// VisionCapable reports whether the active provider/model can process images
// (BlackBox Phase 5 capability gate).
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func VisionCapable() bool {
	return activeProvider != nil && activeProvider.Capabilities().Vision
}

// ProviderVisionCapable reports whether a named provider can see; an empty
// name means the active provider.
func ProviderVisionCapable(name string) bool {
	if name == "" {
		return VisionCapable()
	}
	p, err := GetProviderByName(name)
	return err == nil && p.Capabilities().Vision
}

// RunVisionModel sends a multimodal prompt (text + image parts) to the active
// provider. It fails closed when the active model is not vision-capable, so a
// camera frame is never silently discarded or sent to a text-only model.
//
// Args:
//   - prompt: user prompt text.
//   - parts: multimodal blocks (typically one image part).
//
// Returns: trimmed model output or error.
// Complexity: O(1) provider round trip.
func RunVisionModel(prompt string, parts []providers.MessagePart) (string, error) {
	return runVisionModel(prompt, parts, "")
}

// RunVisionModelWithProvider sends a multimodal prompt to a specific
// registered provider (BlackBox Phase 5 dedicated vision-provider routing,
// P5.5), using that provider's default model.
func RunVisionModelWithProvider(prompt string, parts []providers.MessagePart, providerName string) (string, error) {
	return runVisionModel(prompt, parts, providerName)
}

func runVisionModel(prompt string, parts []providers.MessagePart, providerName string) (string, error) {
	p := activeProvider
	model := activeModel
	if providerName != "" {
		var err error
		p, err = GetProviderByName(providerName)
		if err != nil {
			return "", fmt.Errorf("vision provider %q: %w", providerName, err)
		}
		model = p.DefaultModel()
	}
	if p == nil {
		return "", fmt.Errorf("no AI provider configured")
	}
	if !p.Capabilities().Vision {
		return "", fmt.Errorf(
			"the model %q (%s) does not support vision", model, p.Name())
	}

	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultChatTimeout)
	defer cancel()

	unreg := utils.RegisterOperation(cancel)
	defer unreg()

	temp := float64(DefaultModelConfig().Temperature)
	req := providers.ChatRequest{
		Model: model,
		Messages: []providers.ChatMessage{{
			Role:    "user",
			Content: prompt,
			Parts:   parts,
		}},
		Temperature: &temp,
		MaxTokens:   DefaultModelConfig().MaxTokens,
	}

	out, err := providers.CollectChat(ctx, p, req)
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

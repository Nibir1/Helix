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
	return runModelKind(KindChat, prompt, config, timeout)
}

// runModelKind is RunModelWithTimeout plus the accounting label. The label is
// an explicit parameter rather than inferred state because the planner reaches
// this path through RunPlannerWithRetry, and a session whose /cost report
// blames "chat" for every planner retry explains nothing.
func runModelKind(
	kind CallKind, prompt string, config ModelConfig, timeout time.Duration,
) (string, error) {
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

	started := time.Now()
	out, err := providers.CollectChat(ctx, provider, req)
	RecordCall(kind, provider.Name(), model, prompt, out, time.Since(started), err)
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
	started := time.Now()
	// One accounting entry per streamed call, on whichever branch ends it —
	// including cancellation, which still consumed the prompt.
	finish := func(cause error) (string, error) {
		text := strings.TrimSpace(b.String())
		RecordCall(KindChat, provider.Name(), model, prompt, text, time.Since(started), cause)
		noteCallResult(cause, probing)
		return text, cause
	}

	for {
		select {
		case <-ctx.Done():
			// Ctrl+C or timeout: keep whatever was already rendered.
			return finish(ctx.Err())

		case chunk, ok := <-ch:
			if !ok {
				return finish(nil)
			}
			if chunk.Error != nil {
				return finish(chunk.Error)
			}
			if chunk.Content != "" {
				b.WriteString(chunk.Content)
				if onChunk != nil {
					onChunk(chunk.Content)
				}
			}
			if chunk.Done {
				return finish(nil)
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
	started := time.Now()
	res, err := providers.CollectChatResult(ctx, provider, providers.ChatRequest{
		Model:       model,
		Messages:    []providers.ChatMessage{{Role: "user", Content: prompt}},
		Temperature: &temp,
		MaxTokens:   config.MaxTokens,
		Tools:       tools,
		ToolChoice:  choice,
	})
	// Tool-call arguments are the response here: billing only the assistant
	// prose would under-report a planner turn to near zero.
	RecordCall(KindTool, provider.Name(), model, prompt,
		res.Text+toolCallText(res.ToolCalls), time.Since(started), err)
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
// It resolves the SELECTED model, exactly as ToolCallingAvailable does a few
// lines above. It used to read activeProvider.Capabilities(), and every
// implementation of that method computes its flags from the provider's DEFAULT
// model — so picking gpt-4o and running /eyes on asked "can the default model
// see?" and refused on the answer. The two capability gates in this file now
// resolve the model the same way.
//
// Args: none.
// Returns: bool.
// Complexity: O(1).
func VisionCapable() bool {
	p, model, _ := resolveProvider()
	if p == nil {
		return false
	}
	if model == "" {
		model = p.DefaultModel()
	}
	return providers.SupportsVision(p.Name(), model)
}

// ProviderVisionCapable reports whether a named provider can see; an empty
// name means the active provider.
//
// For a NAMED provider there is no selected model — vision.provider is a
// dedicated routing target that runVisionModel calls with that provider's
// default model — so the default is the honest thing to test here.
func ProviderVisionCapable(name string) bool {
	if name == "" {
		return VisionCapable()
	}
	p, err := GetProviderByName(name)
	if err != nil {
		return false
	}
	return providers.SupportsVision(p.Name(), p.DefaultModel())
}

// visionMaxTokens is the answer budget for a vision call, deliberately larger
// than the 512-token chat default.
//
// Reasoning models spend the budget BEFORE they answer, and they spend more of
// it on an image than on text. Measured against Helix's own default local model
// (gemma4:e2b, a thinking build): at 512 tokens it produced ~770 characters of
// private reasoning and then stopped, emitting no answer at all — which reached
// the user as "The vision model returned nothing." At 1024 the same prompt and
// image answered correctly and repeatably.
//
// This is a floor on capability, not a target: a model that answers in one
// sentence still costs one sentence.
const visionMaxTokens = 1024

// ModelVisionCapable reports whether a specific provider/model pair can see.
// Used by the vision.model override, where neither the active chat model nor
// the provider default is the model that will actually run.
func ModelVisionCapable(provider, model string) bool {
	return providers.SupportsVision(provider, model)
}

// VisionCapableProviders returns the registered providers that could serve as
// vision.provider, using each one's default model.
//
// It exists so the /eyes refusal can name real options instead of telling the
// user to "set one first" and leaving them to guess which provider
// qualifies — the same reasoning that made the voice wizard print valid voices at
// the moment of asking.
//
// Args: none.
// Returns: sorted provider names (nil before the registry is built).
// Complexity: O(providers).
func VisionCapableProviders() []string {
	if registry == nil {
		return nil
	}
	var out []string
	for _, name := range registry.Names() {
		p, err := registry.Get(name)
		if err != nil {
			continue
		}
		if providers.SupportsVision(name, p.DefaultModel()) {
			out = append(out, name)
		}
	}
	return out
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
	return runVisionModelOn(prompt, parts, providerName, "")
}

// RunVisionModelOn sends a multimodal prompt to a specific provider AND model.
//
// The model override exists because vision and chat want different things from
// the same session. Chat wants the biggest model the machine can hold; the
// companion loop wants the fastest one that can describe a frame, because it
// runs on a timer and competes with the conversation for the same runtime. A
// 5B general model measured 9-20s per frame here where a purpose-built ~2B VLM
// is the entire reason the loop is affordable.
//
// Args: providerName ("" = active), model ("" = the provider's usual choice).
func RunVisionModelOn(prompt string, parts []providers.MessagePart, providerName, model string) (string, error) {
	return runVisionModelOn(prompt, parts, providerName, model)
}

func runVisionModelOn(prompt string, parts []providers.MessagePart, providerName, model string) (string, error) {
	p := activeProvider
	if model == "" {
		model = activeModel
	}
	if providerName != "" {
		var err error
		p, err = GetProviderByName(providerName)
		if err != nil {
			return "", fmt.Errorf("vision provider %q: %w", providerName, err)
		}
		if model == "" || providerName != ActiveProviderName() {
			// A named provider's active-chat model is meaningless to it.
			model = p.DefaultModel()
		}
	}
	if p == nil {
		return "", fmt.Errorf("no AI provider configured")
	}
	// Two independent ways to qualify, and either is enough.
	//
	// SupportsVision asks about the model ACTUALLY being sent, which is what
	// Capabilities() cannot do — it computes its flags from the provider's
	// DEFAULT model, so with a selected model or a vision.model override in
	// play it answers a question nobody asked. But name matching only knows
	// the families in the catalog, and a custom endpoint's bespoke model name
	// matches nothing while the provider itself may know perfectly well that it
	// can see.
	//
	// So this fails OPEN, deliberately. A false accept costs one call to a
	// model that ignores the image; a false reject makes a working camera
	// unreachable — which is the failure this codebase has now hit twice.
	if !providers.SupportsVision(p.Name(), model) && !p.Capabilities().Vision {
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
		MaxTokens:   visionMaxTokens,
	}

	started := time.Now()
	out, err := providers.CollectChat(ctx, p, req)
	RecordCall(KindVision, p.Name(), model, prompt, out, time.Since(started), err)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// toolCallText flattens tool-call names and arguments into the text the meter
// bills as the response.
func toolCallText(calls []providers.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	for _, c := range calls {
		b.WriteString(c.Name)
		b.WriteString(c.Arguments)
	}
	return b.String()
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

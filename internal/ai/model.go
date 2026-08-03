// internal/ai/model.go
// Purpose: Local and remote model execution abstraction.
// Phase 0 fix: remove destructive first-line truncation that could destroy
// strict JSON planner output. Add planner-specific inference configuration.
package ai

import (
	"fmt"
	"os"
	"strings"

	llama "github.com/go-skynet/go-llama.cpp"
)

// model is the loaded local llama.cpp model.
// It remains nil when OpenAI or mock mode is active.
var model *llama.LLama

// ModelConfig holds inference parameters.
// StopWords is intentionally excluded from JSON persistence.
type ModelConfig struct {
	Temperature float32  `json:"temperature"`
	TopP        float32  `json:"top_p"`
	TopK        int      `json:"top_k"`
	MaxTokens   int      `json:"max_tokens"`
	StopWords   []string `json:"-"`
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

// PlannerModelConfig returns settings optimized for strict JSON planner output.
// It uses lower temperature and a much larger token budget so JSON is not cut off.
func PlannerModelConfig() ModelConfig {
	return ModelConfig{
		Temperature: 0.2,
		TopP:        0.95,
		TopK:        40,
		MaxTokens:   2048,
		StopWords:   nil,
	}
}

// LoadModel loads a GGUF model for local inference.
func LoadModel(modelPath string) error {
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("model not found at %s", modelPath)
	}

	var err error
	model, err = llama.New(
		modelPath,
		llama.EnableF16Memory,
		llama.SetContext(2048),
		llama.SetNBatch(512),
	)
	if err != nil {
		return fmt.Errorf("failed to load model: %w", err)
	}

	fmt.Printf("Model loaded successfully: %s\n", modelPath)
	return nil
}

// RunModel runs a general-purpose model request.
func RunModel(prompt string) (string, error) {
	return RunModelWithConfig(prompt, DefaultModelConfig())
}

// RunPlannerModel runs a planner-specific request.
// This must never truncate JSON output.
func RunPlannerModel(prompt string) (string, error) {
	return RunModelWithConfig(prompt, PlannerModelConfig())
}

// RunModelWithConfig runs the active provider with explicit inference settings.
func RunModelWithConfig(prompt string, config ModelConfig) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	// Prevent zero/negative max tokens from reaching providers.
	if config.MaxTokens <= 0 {
		config.MaxTokens = DefaultModelConfig().MaxTokens
	}

	// Remote OpenAI path.
	if GetProvider() == ProviderOpenAI {
		return runWithOpenAI(prompt, config)
	}

	// Local llama.cpp path.
	if model == nil {
		return "", fmt.Errorf("model not loaded")
	}

	opts := []llama.PredictOption{
		llama.SetTemperature(config.Temperature),
		llama.SetTopP(config.TopP),
		llama.SetTopK(config.TopK),
		llama.SetTokens(config.MaxTokens),
	}

	// Only set stop words when explicitly requested.
	// Planner mode intentionally uses none.
	if len(config.StopWords) > 0 {
		opts = append(opts, llama.SetStopWords(config.StopWords...))
	}

	out, err := model.Predict(prompt, opts...)
	if err != nil {
		return "", fmt.Errorf("prediction failed: %w", err)
	}

	out = strings.TrimSpace(out)

	// Remove common assistant prefixes, but never truncate structured output.
	prefixes := []string{"Assistant:", "AI:", "Response:"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(out, prefix) {
			out = strings.TrimPrefix(out, prefix)
			out = strings.TrimSpace(out)
		}
	}

	return out, nil
}

// CloseModel releases local model resources.
func CloseModel() {
	if model != nil {
		model = nil
	}
}

// ModelIsLoaded reports whether a local model is loaded.
func ModelIsLoaded() bool {
	return model != nil
}

// TestModelWithSimplePrompt is a lightweight runtime sanity check.
func TestModelWithSimplePrompt() (string, error) {
	if GetProvider() == ProviderOpenAI {
		prompt := "Say 'Hello world' in one short sentence."
		return runWithOpenAI(prompt, DefaultModelConfig())
	}

	if model == nil {
		return "", fmt.Errorf("model not loaded")
	}

	prompt := "User: Say 'Hello world'\nAssistant: Hello world"

	opts := []llama.PredictOption{
		llama.SetTemperature(0.1),
		llama.SetTopP(0.5),
		llama.SetTopK(10),
		llama.SetTokens(10),
	}

	response, err := model.Predict(prompt, opts...)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(response), nil
}

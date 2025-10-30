package ai

import (
	"fmt"
	"os"
	"strings"

	llama "github.com/go-skynet/go-llama.cpp"
)

var model *llama.LLama

// ModelConfig holds parameters for AI model inference
type ModelConfig struct {
	Temperature float32
	TopP        float32
	TopK        int
	MaxTokens   int
}

// DefaultModelConfig returns optimized settings for CLI assistance
func DefaultModelConfig() ModelConfig {
	return ModelConfig{
		Temperature: 0.7,
		TopP:        0.9,
		TopK:        40,
		MaxTokens:   150,
	}
}

// LoadModel loads the GGUF model with better error handling
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

	fmt.Printf("✅ Model loaded successfully: %s\n", modelPath)
	return nil
}

// RunModel queries the model with enhanced parameters
func RunModel(prompt string) (string, error) {
	return RunModelWithConfig(prompt, DefaultModelConfig())
}

// RunModelWithConfig runs the model with custom parameters
func RunModelWithConfig(prompt string, config ModelConfig) (string, error) {
	if model == nil {
		return "", fmt.Errorf("model not loaded")
	}

	// Enhanced cleaning
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	// ACTUALLY USE the config parameter instead of hardcoded values
	opts := []llama.PredictOption{
		llama.SetTemperature(config.Temperature), // USE CONFIG
		llama.SetTopP(config.TopP),               // USE CONFIG
		llama.SetTopK(config.TopK),               // USE CONFIG
		llama.SetTokens(config.MaxTokens),        // USE CONFIG
		llama.SetStopWords("\n", "```", "`"),
	}

	out, err := model.Predict(prompt, opts...)
	if err != nil {
		return "", fmt.Errorf("prediction failed: %w", err)
	}

	// Less aggressive cleaning - preserve meaningful responses
	out = strings.TrimSpace(out)

	// Only take first line if response is very long
	if len(out) > 200 {
		lines := strings.Split(out, "\n")
		if len(lines) > 0 {
			out = strings.TrimSpace(lines[0])
		}
	}

	// Remove common prefixes but be more lenient
	prefixes := []string{"Assistant:", "AI:", "Response:"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(out, prefix) {
			out = strings.TrimPrefix(out, prefix)
			out = strings.TrimSpace(out)
		}
	}

	return out, nil
}

// CloseModel frees resources
func CloseModel() {
	if model != nil {
		model = nil
	}
}

// ModelIsLoaded checks if the model is ready
func ModelIsLoaded() bool {
	return model != nil
}

func TestModelWithSimplePrompt() (string, error) {
	if model == nil {
		return "", fmt.Errorf("model not loaded")
	}

	// Very simple, constrained prompt
	prompt := "User: Say 'Hello world'\nAssistant: Hello world"

	// Very restrictive parameters
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

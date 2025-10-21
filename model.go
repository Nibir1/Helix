// model.go
package main

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

	// Clean and prepare the prompt
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "", fmt.Errorf("empty prompt")
	}

	// Use enhanced prediction with parameters
	opts := []llama.PredictOption{
		llama.SetTemperature(config.Temperature),
		llama.SetTopP(config.TopP),
		llama.SetTopK(config.TopK),
		llama.SetTokens(config.MaxTokens),
	}

	out, err := model.Predict(prompt, opts...)
	if err != nil {
		return "", fmt.Errorf("prediction failed: %w", err)
	}

	// Clean the output
	out = strings.TrimSpace(out)
	out = strings.TrimPrefix(out, "Assistant:")
	out = strings.TrimSpace(out)

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

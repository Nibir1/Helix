package main

import (
	"fmt"
	"os"

	llama "github.com/go-skynet/go-llama.cpp"
)

var model *llama.LLama

// LoadModel loads the GGUF model
func LoadModel(modelPath string) error {
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return fmt.Errorf("model not found at %s", modelPath)
	}

	var err error
	model, err = llama.New(modelPath)
	if err != nil {
		return fmt.Errorf("failed to load model: %w", err)
	}

	fmt.Println("✅ Model loaded successfully:", modelPath)
	return nil
}

// RunModel queries the model
func RunModel(prompt string) (string, error) {
	if model == nil {
		return fmt.Sprintf("(mock) You asked: %s", prompt), nil
	}

	// Call Predict with the prompt; omit the context and the incorrect option struct
	out, err := model.Predict(prompt)
	if err != nil {
		return "", fmt.Errorf("prediction failed: %w", err)
	}

	return out, nil
}

// CloseModel frees resources
func CloseModel() {
	if model != nil {
		// LLama does not expose a Close method on the pointer type in this binding,
		// so drop our reference to allow GC/finalizers in the underlying library to run.
		model = nil
	}
}

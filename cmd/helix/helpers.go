// cmd/helix/helpers.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/llamacpp"
	"helix/internal/ollama"
	"helix/internal/providers"

	"github.com/fatih/color"
)

type providerOption struct {
	ID    string
	Label string
}

var providerOptions = []providerOption{
	{ID: "openai", Label: "OpenAI"},
	{ID: "anthropic", Label: "Anthropic"},
	{ID: "deepseek", Label: "DeepSeek"},
	{ID: "kimi", Label: "Kimi"},
	{ID: "qwen", Label: "Qwen"},
	{ID: "glm", Label: "GLM"},
	{ID: "ollama", Label: "Ollama (local)"},
	{ID: "llamacpp", Label: "llama.cpp server (local)"},
	{ID: "custom", Label: "Custom OpenAI-compatible endpoint"},
}

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))

	switch name {
	case "local", "llama", "llama.cpp", "llamacpp":
		return "llamacpp"
	case "openai", "anthropic", "deepseek", "kimi", "qwen", "glm", "ollama", "custom":
		return name
	default:
		return name
	}
}

func parseProviderNumber(line string) (int, error) {
	var n int

	if _, err := fmt.Sscanf(line, "%d", &n); err != nil {
		return 0, err
	}

	if n < 1 || n > len(providerOptions) {
		return 0, fmt.Errorf("provider number out of range")
	}

	return n - 1, nil
}

func setupProvider(provider string) error {
	provider = normalizeProviderName(provider)

	switch provider {
	case "openai", "anthropic", "deepseek", "kimi", "qwen", "glm":
		return ensureRemoteAPIKey(provider)

	case "custom":
		return setupCustomProvider()

	case "ollama":
		return setupOllamaProvider()

	case "llamacpp":
		return setupLlamaCppProvider()

	default:
		return fmt.Errorf("unknown provider: %s", provider)
	}
}

func ensureRemoteAPIKey(provider string) error {
	if ai.ProviderHasSavedKey(provider) {
		if commands.AskForConfirmation(fmt.Sprintf("Use saved API key for %s?", provider)) {
			return nil
		}
	}

	key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("Paste API key for %s", provider)))
	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}

	return ai.SaveProviderKey(provider, key)
}

func setupCustomProvider() error {
	baseURL := strings.TrimSpace(commands.AskLine("Custom OpenAI-compatible base URL (example: https://api.example.com/v1)"))
	if baseURL == "" {
		return fmt.Errorf("custom base URL cannot be empty")
	}

	key := strings.TrimSpace(commands.AskLine("Custom API key (optional)"))

	if err := ai.RegisterCustomProvider(baseURL, key); err != nil {
		return err
	}

	if key != "" {
		if err := ai.SaveProviderKey("custom", key); err != nil {
			return err
		}
	}

	cfg.CustomProviderBaseURL = baseURL
	return nil
}

func setupOllamaProvider() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !ollama.IsInstalled() {
		if !commands.AskForConfirmation("Ollama not found. Install Ollama now?") {
			return fmt.Errorf("ollama is not installed")
		}

		installCtx, installCancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer installCancel()

		if err := ollama.Install(installCtx); err != nil {
			return err
		}
	}

	return ollama.EnsureRunning(ctx)
}

func selectModelForProvider(provider string) error {
	provider = normalizeProviderName(provider)

	switch provider {
	case "ollama":
		return selectOllamaModel()

	case "llamacpp":
		// llama.cpp model selection happens during runtime setup.
		return nil

	default:
		return selectRemoteModel(provider)
	}
}

func selectRemoteModel(provider string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := ai.ListProviderModels(ctx)
	defaultModel := ai.DefaultModelForProvider(provider)

	if err != nil {
		color.Yellow("Could not fetch live model list: %v", err)
		color.Yellow("Using default model: %s", defaultModel)

		ai.UseModel(defaultModel)
		return nil
	}

	if len(models) == 0 {
		ai.UseModel(defaultModel)
		return nil
	}

	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0].ID
	}

	color.Cyan("Available models:")

	for i, model := range models {
		if i >= 25 {
			color.Cyan("  ... and %d more", len(models)-25)
			break
		}

		color.Cyan("  %s", model.ID)
	}

	choice := strings.TrimSpace(commands.AskLine(fmt.Sprintf("Model ID (default: %s)", defaultModel)))

	if choice == "" {
		choice = defaultModel
	}

	ai.UseModel(choice)
	return nil
}

func selectOllamaModel() error {
	client := ollama.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		return err
	}

	if len(models) == 0 {
		recs := providers.RecommendLocalModels(providers.DetectHardware())

		tag := ""

		for _, rec := range recs {
			if rec.Runtime == "ollama" && rec.OllamaTag != "" {
				tag = rec.OllamaTag
				break
			}
		}

		if tag == "" {
			return fmt.Errorf("no Ollama model recommendation available")
		}

		if !commands.AskForConfirmation(fmt.Sprintf("No Ollama models installed. Pull %s now?", tag)) {
			return fmt.Errorf("no Ollama model selected")
		}

		pullCtx, pullCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer pullCancel()

		if err := client.PullModel(pullCtx, tag, func(status string) {
			color.Blue("Ollama pull: %s", status)
		}); err != nil {
			return err
		}

		ai.UseModel(tag)
		return nil
	}

	color.Cyan("Installed Ollama models:")

	for _, model := range models {
		color.Cyan("  %s", model.ID)
	}

	choice := strings.TrimSpace(commands.AskLine(fmt.Sprintf("Ollama model (default: %s)", models[0].ID)))

	if choice == "" {
		choice = models[0].ID
	}

	if !containsModelID(models, choice) {
		if !commands.AskForConfirmation(fmt.Sprintf("Model %q is not installed. Pull it now?", choice)) {
			return fmt.Errorf("selected Ollama model is not installed")
		}

		pullCtx, pullCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer pullCancel()

		if err := client.PullModel(pullCtx, choice, func(status string) {
			color.Blue("Ollama pull: %s", status)
		}); err != nil {
			return err
		}
	}

	ai.UseModel(choice)
	return nil
}

func containsModelID(models []providers.ModelInfo, id string) bool {
	for _, model := range models {
		if strings.EqualFold(model.ID, id) {
			return true
		}
	}

	return false
}

func setupLlamaCppProvider() error {
	return setupLlamaCppWithModel(context.Background(), "")
}

func setupLlamaCppWithModel(ctx context.Context, model string) error {
	if strings.TrimSpace(model) == "" {
		model = promptForLlamaModel()
	}

	var modelPath string

	switch {
	case strings.HasPrefix(model, "http://"), strings.HasPrefix(model, "https://"):
		if !commands.AskForConfirmation("Download model from URL?") {
			return fmt.Errorf("model download cancelled")
		}

		path, err := llamacpp.EnsureModelFromURL(ctx, model)
		if err != nil {
			return err
		}

		modelPath = path

	case fileExists(model):
		modelPath = model

	default:
		rec, ok := llamacpp.FindModel(model)
		if !ok {
			return fmt.Errorf("model not found: %s", model)
		}

		if !commands.AskForConfirmation(fmt.Sprintf("Download %s?", rec.DisplayName)) {
			return fmt.Errorf("model download cancelled")
		}

		path, err := llamacpp.EnsureModel(ctx, rec)
		if err != nil {
			return err
		}

		modelPath = path
	}

	if err := ai.EnsureLlamaCppServer(ctx, modelPath); err != nil {
		return err
	}

	ai.UseModel("helix-local")
	return nil
}

func promptForLlamaModel() string {
	recs := llamacpp.RecommendedModels()

	color.Cyan("Recommended llama.cpp models:")

	for i, rec := range recs {
		color.Cyan("  %d) %s", i+1, rec.DisplayName)
	}

	color.Cyan("You may also enter a local GGUF path or HTTPS URL.")

	line := strings.TrimSpace(commands.AskLine("Choose model number, ID, path, or URL"))

	if line == "" {
		return recs[0].ID
	}

	if idx, err := parseProviderNumber(line); err == nil && idx < len(recs) {
		return recs[idx].ID
	}

	return line
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}

	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func useProviderInteractive(provider string) error {
	provider = normalizeProviderName(provider)

	if err := setupProvider(provider); err != nil {
		return err
	}

	// FIX: Activate provider BEFORE selecting model
	if err := ai.UseProvider(provider); err != nil {
		return err
	}

	if err := selectModelForProvider(provider); err != nil {
		return err
	}

	return nil
}

func useModelInteractive(provider, model string) error {
	provider = normalizeProviderName(provider)
	model = strings.TrimSpace(model)

	if model == "" {
		return fmt.Errorf("model is empty")
	}

	switch provider {
	case "ollama":
		return ensureOllamaModel(model)

	case "llamacpp":
		return setupLlamaCppWithModel(context.Background(), model)

	default:
		ai.UseModel(model)
		return nil
	}
}

func ensureOllamaModel(model string) error {
	if err := setupOllamaProvider(); err != nil {
		return err
	}

	client := ollama.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		return err
	}

	if containsModelID(models, model) {
		ai.UseModel(model)
		return nil
	}

	if !commands.AskForConfirmation(fmt.Sprintf("Model %q is not installed. Pull it now?", model)) {
		return fmt.Errorf("selected Ollama model is not installed")
	}

	pullCtx, pullCancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer pullCancel()

	if err := client.PullModel(pullCtx, model, func(status string) {
		color.Blue("Ollama pull: %s", status)
	}); err != nil {
		return err
	}

	ai.UseModel(model)
	return nil
}

// cmd/helix/helpers.go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/ollama"
	"helix/internal/providers"
	"helix/internal/providers/llamacpp"
	"helix/internal/rag"
	"helix/internal/utils"

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
	{ID: "xai", Label: "xAI (Grok)"},
	{ID: "ollama", Label: "Ollama (local)"},
	{ID: "llamacpp", Label: "llama.cpp (local, user-managed llama-server)"},
}

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "openai", "anthropic", "deepseek", "kimi", "qwen", "glm", "xai", "ollama":
		return name
	default:
		return name
	}
}

func setupProvider(provider string) error {
	provider = normalizeProviderName(provider)
	switch provider {
	case "openai", "anthropic", "deepseek", "kimi", "qwen", "glm", "xai":
		return ensureRemoteAPIKey(provider)
	case "ollama":
		return setupOllamaProvider()
	case "llamacpp":
		return setupLlamaCppProvider()
	default:
		// The registry is the authority on what exists: a provider registered
		// by ai.InitProviders but missing from this switch used to be listed by
		// /provider status and then rejected by /provider use — which is
		// exactly how llamacpp shipped broken. Anything keyless and registered
		// is usable as-is.
		if ai.HasProvider(provider) {
			return nil
		}
		return fmt.Errorf("unknown provider: %s (registered: %s)",
			provider, strings.Join(ai.ListProviders(), ", "))
	}
}

// setupLlamaCppProvider prepares the user-managed llama-server sidecar. There
// is no key and nothing to install (ADR-002) — the only useful setup step is
// telling the user whether the server is actually reachable, since an
// unreachable one fails later as an opaque connection error.
func setupLlamaCppProvider() error {
	url := llamacpp.BaseURL(cfg.LLM.LlamaCppURL)
	color.Cyan("llama.cpp endpoint: %s", url)

	p, err := ai.GetProviderByName(llamacpp.Name)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if herr := p.HealthCheck(ctx); herr != nil {
		kind, hint := llamacpp.Diagnose(herr, url)
		if kind == llamacpp.DiagnosisForeignServer {
			color.Yellow("A DIFFERENT service is answering on %s.", url)
		} else {
			color.Yellow("llama-server is not reachable at %s.", url)
		}
		for _, line := range strings.Split(hint, "\n") {
			color.Yellow("  %s", line)
		}
		color.Yellow("  Override the endpoint with HELIX_LLAMACPP_URL or llm.llamacpp_url.")
		// Selecting it anyway leaves the shell unable to answer ANYTHING, so
		// say that plainly rather than letting the next prompt fail with a raw
		// 404 from whatever else is on that port.
		color.Yellow("  Until it responds, every planner and chat request will fail.")
		if !commands.AskForConfirmation("Select llama.cpp anyway?") {
			return fmt.Errorf("llama.cpp not usable at %s", url)
		}
		return nil
	}

	color.Green("llama-server reachable.")
	return nil
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
	if !confirmKeyForProvider(provider, key) {
		return fmt.Errorf("API key entry cancelled")
	}

	return ai.SaveProviderKey(provider, key)
}

// setupOllamaProvider ensures Ollama is usable.
//
// Args: none.
// Returns: error when Ollama cannot be installed/started.
// Complexity: O(install/startup time).
func setupOllamaProvider() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	client := ollama.NewClient()
	err := client.Health(ctx)
	cancel()

	if err == nil {
		return nil
	}

	if !ollama.IsInstalled() {
		if !commands.AskForConfirmation("Ollama not found. Install Ollama now?") {
			return fmt.Errorf("ollama is not installed")
		}

		installErr := runCancellableProgressWithTimeout(
			"INSTALLING OLLAMA",
			30*time.Minute,
			func(ctx context.Context, progress func(string, int64, int64)) error {
				progress("INSTALLING OLLAMA", 0, 0)
				return ollama.Install(ctx)
			},
		)

		if installErr != nil {
			return installErr
		}
	}

	return runCancellableProgressWithTimeout(
		"STARTING OLLAMA",
		2*time.Minute,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			progress("STARTING OLLAMA", 0, 0)
			return ollama.EnsureRunning(ctx)
		},
	)
}

func selectModelForProvider(provider string) error {
	provider = normalizeProviderName(provider)
	switch provider {
	case "ollama":
		return selectOllamaModel()
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

// selectOllamaModel lets the user choose any installed or pullable Ollama model.
//
// Args: none.
// Returns: error when model selection/pull fails.
// Complexity: O(model pull time) when a pull is required.
func selectOllamaModel() error {
	client := ollama.NewClient()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	models, err := client.ListModels(ctx)
	if err != nil {
		return err
	}

	recs := providers.RecommendLocalModels(providers.DetectHardware())
	defaultTag := ""

	for _, rec := range recs {
		if rec.Runtime == "ollama" && rec.OllamaTag != "" {
			defaultTag = rec.OllamaTag
			break
		}
	}

	if len(models) > 0 {
		color.Cyan("Installed Ollama models:")
		for _, model := range models {
			color.Cyan("  %s", model.ID)
		}

		if defaultTag == "" {
			defaultTag = models[0].ID
		}
	} else {
		color.Yellow("No Ollama models are installed.")
		if defaultTag != "" {
			color.Cyan("Recommended model: %s", defaultTag)
		}
	}

	color.Cyan("Enter any Ollama model tag (for example: gemma4:e2b, phi4-mini, llama3.1:8b, qwen3:4b).")

	choice := strings.TrimSpace(
		commands.AskLine(fmt.Sprintf("Ollama model (default: %s)", defaultTag)),
	)

	if choice == "" {
		if defaultTag == "" {
			return fmt.Errorf("no Ollama model selected")
		}
		choice = defaultTag
	}

	if containsModelID(models, choice) {
		ai.UseModel(choice)
		return nil
	}

	if !commands.AskForConfirmation(fmt.Sprintf("Model %q is not installed. Pull it now?", choice)) {
		return fmt.Errorf("selected Ollama model is not installed")
	}

	err = runCancellableProgressWithTimeout(
		"PULLING OLLAMA MODEL",
		1*time.Hour,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			return client.PullModel(ctx, choice, func(status string, completed, total int64) {
				stage := "PULLING " + strings.ToUpper(choice)
				lcStatus := strings.ToLower(status)
				if strings.Contains(lcStatus, "verifying") {
					stage = "VERIFYING " + strings.ToUpper(choice)
				} else if strings.Contains(lcStatus, "writing") || strings.Contains(lcStatus, "manifest") {
					stage = "FINALIZING " + strings.ToUpper(choice)
				}
				progress(stage, completed, total)
			})
		},
	)
	if err != nil {
		return err
	}

	// CRITICAL FIX: Actually activate the pulled model so Helix doesn't
	// leak the previous provider's model.
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
	default:
		ai.UseModel(model)
		return nil
	}
}

// ensureOllamaModel ensures a specific Ollama model is installed and active.
//
// Args:
//   - model: Ollama model tag.
//
// Returns: error when the model cannot be selected/pulled.
// Complexity: O(model pull time) when a pull is required.
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

	err = runCancellableProgressWithTimeout(
		"PULLING OLLAMA MODEL",
		1*time.Hour,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			return client.PullModel(ctx, model, func(status string, completed, total int64) {
				stage := "PULLING " + strings.ToUpper(model)
				lcStatus := strings.ToLower(status)
				if strings.Contains(lcStatus, "verifying") {
					stage = "VERIFYING " + strings.ToUpper(model)
				} else if strings.Contains(lcStatus, "writing") || strings.Contains(lcStatus, "manifest") {
					stage = "FINALIZING " + strings.ToUpper(model)
				}
				progress(stage, completed, total)
			})
		},
	)
	if err != nil {
		return err
	}

	// CRITICAL FIX: Activate the pulled model.
	ai.UseModel(model)
	return nil
}

// runCancellableProgressWithTimeout runs fn with a timeout, progress bar, and Ctrl+C support.
//
// Args:
//   - title: default progress stage title.
//   - timeout: maximum operation duration.
//   - fn: cancellable operation.
//
// Returns: error from fn.
// Complexity: O(operation runtime).
func runCancellableProgressWithTimeout(
	title string,
	timeout time.Duration,
	fn func(ctx context.Context, progress func(string, int64, int64)) error,
) error {
	parent := context.Background()

	if timeout > 0 {
		var cancel context.CancelFunc
		parent, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}

	return runCancellableProgressWithBase(parent, title, fn)
}

// runCancellableProgressWithBase is the shared progress/interrupt implementation.
func runCancellableProgressWithBase(
	parent context.Context,
	title string,
	fn func(ctx context.Context, progress func(string, int64, int64)) error,
) error {
	ctx, cancel := context.WithCancel(parent)
	unreg := utils.RegisterOperation(cancel)

	prog := rag.NewProgress()
	prog.SetStage(title)
	prog.Start()

	// Track the last stage to avoid redundant updates
	lastStage := title
	lastCurrent := int64(0)
	lastTotal := int64(0)

	cb := func(stage string, current, total int64) {
		if stage == "" {
			stage = title
		}

		// Only update if something changed to reduce flicker
		if stage != lastStage || current != lastCurrent || total != lastTotal {
			if total > 0 {
				if current < 0 {
					current = 0
				}
				if current > total {
					current = total
				}
				prog.Set(stage, int(current), int(total))
			} else {
				prog.SetStage(stage)
			}
			lastStage = stage
			lastCurrent = current
			lastTotal = total
		}
	}

	err := fn(ctx, cb)

	prog.Stop()
	unreg()
	cancel()

	if errors.Is(err, context.Canceled) {
		color.Yellow("Operation cancelled.")
	}

	if errors.Is(err, context.DeadlineExceeded) {
		color.Yellow("Operation timed out.")
	}

	return err
}

// confirmKeyForProvider warns when a pasted key was plainly issued by another
// vendor, and asks before storing it. Warn-and-confirm, never a hard block:
// key formats change, and refusing a valid new format would be worse than the
// mistake being guarded against.
//
// Returns false when the user declines, so the caller can abort cleanly.
func confirmKeyForProvider(provider, key string) bool {
	owner, wrong := providers.MisdirectedKey(provider, key)
	if !wrong {
		return true
	}
	color.Yellow("That key looks like a %s key, but you are configuring %q.", owner, provider)
	color.Yellow("  %s", providers.KeyOwnerHint(provider, owner))
	return commands.AskForConfirmation("Store it anyway?")
}

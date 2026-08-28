// cmd/helix/helpers.go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/ollama"
	"helix/internal/providers"
	"helix/internal/providers/llamacpp"
	"helix/internal/rag"
	"helix/internal/shell"
	"helix/internal/utils"
)

type providerOption struct {
	ID    string
	Label string
}

// providerOptions is the FIRST-RUN menu, not the list of what Helix supports.
//
// llama.cpp is deliberately absent. It is still registered, still a valid
// failover target, and still selectable with `/provider use llamacpp` — but it
// does not belong in the choice a new user makes before anything works, because
// picking it there is a commitment to installing a runtime, obtaining a GGUF
// that a given build can load, and launching a server by hand. Ollama does the
// same job with none of that, and Ollama is itself built on llama.cpp, so the
// engine is present either way.
//
// The menu is for getting to a working shell. Everything registered stays
// reachable afterwards; `/provider list` shows the full set.
var providerOptions = []providerOption{
	{ID: "openai", Label: "OpenAI"},
	{ID: "anthropic", Label: "Anthropic"},
	{ID: "gemini", Label: "Google Gemini"},
	{ID: "meta", Label: "Meta (Muse Spark)"},
	{ID: "deepseek", Label: "DeepSeek"},
	{ID: "kimi", Label: "Kimi"},
	{ID: "qwen", Label: "Qwen"},
	{ID: "glm", Label: "GLM"},
	{ID: "xai", Label: "xAI (Grok)"},
	{ID: "ollama", Label: "Ollama (local)"},
}

func normalizeProviderName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "openai", "anthropic", "gemini", "meta",
		"deepseek", "kimi", "qwen", "glm", "xai", "ollama":
		return name
	default:
		return name
	}
}

func setupProvider(provider string) error {
	provider = normalizeProviderName(provider)
	switch provider {
	case "openai", "anthropic", "gemini", "meta",
		"deepseek", "kimi", "qwen", "glm", "xai":
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
	fmt.Println(shell.KV("ENDPOINT", shell.Value(url), shell.KVWidth("ENDPOINT")))

	p, err := ai.GetProviderByName(llamacpp.Name)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if herr := p.HealthCheck(ctx); herr != nil {
		// Record it, do not just print it. This probe is the only hard evidence
		// Helix has before the first model call, and the per-turn status line used
		// to report CLEAR right after this very warning because the failover
		// breaker had not yet seen two failed calls.
		ai.NoteProviderUnreachable(llamacpp.Name, herr.Error())

		kind, hint := llamacpp.Diagnose(herr, url)
		if kind == llamacpp.DiagnosisForeignServer {
			uiWarn("wrong service", "something else is answering on "+url)
		} else {
			uiWarn("llama-server", "not reachable at "+url)
		}
		for _, line := range strings.Split(hint, "\n") {
			uiDetail(line)
		}

		// Whether llama-server exists decides the advice, and Diagnose already
		// made that call (it is the package that can check). Reuse it here so
		// /provider-status and this wizard cannot disagree.
		_, installed := llamacpp.ServerInstalled()
		uiDetail("Override the endpoint with HELIX_LLAMACPP_URL or llm.llamacpp_url.")

		// Offer to install it, then walk the user to a running server. Each stage
		// only runs when the previous one is satisfied, so nobody is shown a
		// launch command for a binary they do not have, or a model list for a
		// runtime that cannot start.
		if !installed {
			installed = offerLlamaCppInstall()
		}
		if installed {
			guideLlamaCppModel(llamacpp.BaseURL(cfg.LLM.LlamaCppURL))
		} else {
			// No binary: the Ollama weights are context for later, and a working
			// Ollama is a genuine alternative worth naming.
			suggestOllamaInstead(false)
			suggestOllamaWeightsForLlamaCpp(false)
		}

		// Selecting it anyway leaves the shell unable to answer ANYTHING, so
		// say that plainly rather than letting the next prompt fail with a raw
		// 404 from whatever else is on that port.
		uiWarn("unusable", "until llama-server responds, every planner and chat request fails")
		if !commands.AskForConfirmation("Select llama.cpp anyway?") {
			return fmt.Errorf("llama.cpp not usable at %s", url)
		}
		return nil
	}

	ai.NoteProviderReachable(llamacpp.Name)
	uiOK("llama-server", "reachable")
	reportResolvedLocalModel()
	return nil
}

// reportResolvedLocalModel replaces the placeholder model label with whatever the
// local runtime says it has loaded, and tells the user what that turned out to
// be.
//
// This is not cosmetic. "local-gguf" is a display placeholder that was also
// being used as the capability key, so while it was active Helix assumed an 8k
// context window and no vision support regardless of what was really loaded.
// Resolving it is what makes vision work against a multimodal GGUF and stops RAG
// context from being clamped on a 128k model.
func reportResolvedLocalModel() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resolved, changed := ai.ResolveActiveLocalModel(ctx)
	if !changed {
		if ai.IsPlaceholderModel(ai.ActiveModel()) {
			uiWarn("no model name", fmt.Sprintf(
				"the server did not report one — keeping the %q label", ai.ActiveModel()))
			uiDetail("Capability limits fall back to conservative defaults (8k context, no vision).")
			uiUsage("/model use <name>   sets the real one if you know it")
		}
		return
	}

	uiOK("loaded", resolved)
	uiDetail(fmt.Sprintf("context window: %d tokens", providers.GetContextLimit(resolved)))
	if providers.SupportsVision(llamacpp.Name, resolved) {
		uiDetail("vision: supported — /blackbox on will see with this model")
	} else {
		uiDetail("vision: not detected in the model name — the camera stays off")
	}
	cfg.ProviderModel = resolved
	_ = cfg.SavePreferences()
}

func ensureRemoteAPIKey(provider string) error {
	// Do not ask a question the machine can answer for itself. A saved key is
	// verified first, and only a key the provider actually REJECTS sends the
	// user back to their dashboard — "use the saved key?" was a prompt whose
	// right answer Helix already knew.
	if ai.ProviderHasSavedKey(provider) {
		switch verifyProviderKey(provider) {
		case keyWorks:
			return nil
		case keyUnverifiable:
			// A network blip or a provider outage is not a bad key. Making
			// someone re-paste a working credential because their wifi dropped
			// would be the worse failure.
			uiIdle(provider, "keeping the saved key — it could not be checked just now")
			return nil
		case keyRejected:
			uiWarn(provider, "the saved key no longer works — enter a new one")
		}
	}

	key := strings.TrimSpace(commands.AskLine(fmt.Sprintf("Paste API key for %s", provider)))
	if key == "" {
		return fmt.Errorf("API key cannot be empty")
	}
	if !confirmKeyForProvider(provider, key) {
		return fmt.Errorf("API key entry cancelled")
	}

	if err := ai.SaveProviderKey(provider, key); err != nil {
		return err
	}
	verifyProviderKey(provider)
	return nil
}

// keyVerdict is what a credential probe actually established.
//
// Three outcomes, not two: "did not work" and "could not be checked" call for
// opposite responses, and collapsing them is how a dropped wifi connection ends
// up demanding a new API key.
type keyVerdict int

const (
	keyWorks keyVerdict = iota
	keyRejected
	keyUnverifiable
)

// verifyProviderKey probes a provider and reports whether its credential works.
//
// Deliberately not fatal. A network blip, a proxy, or a provider outage would
// otherwise block setup over something that may be fine in a minute — and the
// user has already made their choice. Saying clearly what failed, and letting
// them proceed, beats refusing to continue.
func verifyProviderKey(provider string) keyVerdict {
	err := runCancellableProgressWithTimeout(
		"VERIFYING "+strings.ToUpper(provider),
		30*time.Second,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			progress("VERIFYING "+strings.ToUpper(provider), 0, 0)
			p, gerr := ai.GetProviderByName(provider)
			if gerr != nil {
				return gerr
			}
			return p.HealthCheck(ctx)
		},
	)
	if err == nil {
		uiOK(provider, "key verified")
		ai.NoteProviderReachable(provider)
		return keyWorks
	}

	ai.NoteProviderUnreachable(provider, err.Error())
	if isAuthFailure(err) {
		uiFail(provider, "rejected the API key")
		uiDetail("Check it on the provider's dashboard, then re-run /setup.")
		return keyRejected
	}
	uiWarn(provider, "could not be verified right now: "+err.Error())
	uiDetail("The key is saved; /provider-status re-checks it.")
	return keyUnverifiable
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

	// A local runtime's "default model" is a placeholder for whatever it has
	// loaded. Offering that as the default would persist a label that makes
	// Helix mis-read the model's context window and vision support, so prefer
	// the first real name the runtime reports.
	if ai.IsPlaceholderModel(defaultModel) && err == nil && len(models) > 0 {
		defaultModel = models[0].ID
	}

	if err != nil {
		// A model list that cannot be fetched is a reachability fact about the
		// provider the user is selecting right now. Recording it keeps the status
		// line from claiming CLEAR on a shell that cannot answer anything.
		ai.NoteProviderUnreachable(provider, err.Error())
		uiWarn("model list", "could not be fetched: "+err.Error())
		uiIdle("using the default", defaultModel)
		ai.UseModel(defaultModel)
		return nil
	}
	ai.NoteProviderReachable(provider)

	if len(models) == 0 {
		ai.UseModel(defaultModel)
		return nil
	}

	if defaultModel == "" && len(models) > 0 {
		defaultModel = models[0].ID
	}

	printModelChoices(models, defaultModel)
	choice := strings.TrimSpace(commands.AskLine(shell.Prompt("model id", defaultModel)))
	if choice == "" {
		choice = defaultModel
	}

	ai.UseModel(choice)
	return nil
}

// printModelChoices renders the provider's catalogue.
//
// The old version printed 25 bare IDs and "... and 101 more", which is the
// worst of both: too long to scan and too short to be complete. Models are
// grouped by family and capability-tagged instead, so the list answers the
// question actually being asked — which of these can see, which is the default,
// and is the one I want even here.
func printModelChoices(models []providers.ModelInfo, defaultModel string) {
	const shown = 24
	fmt.Println(shell.PanelTitle("models"))

	rows := make([][]string, 0, shown)
	for i, m := range models {
		if i >= shown {
			break
		}
		mark := ""
		if m.ID == defaultModel {
			mark = shell.Badge(shell.StateGood, "default")
		}
		caps := []string{}
		if providers.SupportsVision("", m.ID) {
			caps = append(caps, "sees")
		}
		if providers.SupportsToolUse("", m.ID) {
			caps = append(caps, "tools")
		}
		rows = append(rows, []string{
			shell.Value(m.ID), shell.Muted(strings.Join(caps, " · ")), mark,
		})
	}
	for _, l := range shell.Table([]string{"model", "can", ""}, rows) {
		fmt.Println(l)
	}
	if len(models) > shown {
		fmt.Println(shell.PanelGap())
		fmt.Println(shell.PanelLine(shell.Muted(fmt.Sprintf(
			"%d more not shown — any id the provider accepts works here",
			len(models)-shown))))
	}
	fmt.Println(shell.PanelEnd())
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

	fmt.Println(shell.PanelTitle("local models"))
	if len(models) > 0 {
		rows := make([][]string, 0, len(models))
		for _, model := range models {
			mark := ""
			if model.ID == defaultTag {
				mark = shell.Badge(shell.StateGood, "recommended here")
			}
			caps := []string{}
			if providers.SupportsVision("ollama", model.ID) {
				caps = append(caps, "sees")
			}
			if providers.SupportsToolUse("ollama", model.ID) {
				caps = append(caps, "tools")
			}
			rows = append(rows, []string{
				shell.Value(model.ID), shell.Muted(strings.Join(caps, " · ")), mark,
			})
		}
		for _, l := range shell.Table([]string{"installed", "can", ""}, rows) {
			fmt.Println(l)
		}
		if defaultTag == "" {
			defaultTag = models[0].ID
		}
	} else {
		fmt.Println(shell.PanelLine(shell.Badge(shell.StateWarn, "nothing installed yet")))
		if defaultTag != "" {
			fmt.Println(shell.PanelLine(shell.Muted("best fit for this hardware: ") +
				shell.Value(defaultTag)))
		}
	}
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelLine(shell.Muted(
		"any tag works — gemma4:e2b · phi4-mini · llama3.1:8b · qwen3:4b")))
	fmt.Println(shell.PanelEnd())

	choice := strings.TrimSpace(commands.AskLine(shell.Prompt("ollama model", defaultTag)))

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
		reportPullFailure(choice, err)
		return nil
	}

	// CRITICAL FIX: Actually activate the pulled model so Helix doesn't
	// leak the previous provider's model.
	ai.UseModel(choice)
	return nil
}

// reportPullFailure explains a failed download and lets setup continue.
//
// Returning the error here used to abort setup, which aborted main, which
// dropped the user back to their login shell — Helix exited because a DOWNLOAD
// did not finish. A real first run hit exactly that: Ollama's registry answered
// 503 through its proxy, and a working terminal became "Setup failed:" and a
// zsh prompt.
//
// Nothing about a missing model justifies that. The provider is chosen, the
// config is written, and every other command still works; what is missing is a
// model, which /setup or `ollama pull` can supply later. Helix's own rule for
// live mode is "degrade, never refuse the whole mode", and it applies here with
// more force, because this is the first thing a new user ever sees.
func reportPullFailure(model string, err error) {
	kind, lines := ollama.DiagnosePull(model, err)

	fmt.Println()
	fmt.Println(shell.PanelTitle("model not downloaded"))
	for i, l := range lines {
		colour := shell.Muted
		if i == 0 {
			colour = func(t string) string { return shell.Fg(shell.HexText, t) }
		}
		for _, w := range shell.PanelWrap(l, colour) {
			fmt.Println(w)
		}
	}
	fmt.Println(shell.PanelGap())
	if ollama.Retryable(kind) {
		fmt.Println(shell.PanelLine(shell.Muted(
			"Helix starts anyway. Run /setup to try again once it is back.")))
	} else {
		fmt.Println(shell.PanelLine(shell.Muted(
			"Helix starts anyway. Run /setup to choose a different model or provider.")))
	}
	fmt.Println(shell.PanelEnd())
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
		reportPullFailure(model, err)
		return nil
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
		uiIdle("cancelled", "")
	}

	if errors.Is(err, context.DeadlineExceeded) {
		uiWarn("timed out", "")
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
	uiWarn("wrong vendor", fmt.Sprintf("that looks like a %s key, but you are configuring %q", owner, provider))
	uiDetail(providers.KeyOwnerHint(provider, owner))
	return commands.AskForConfirmation("Store it anyway?")
}

// suggestOllamaWeightsForLlamaCpp offers the GGUFs Ollama already downloaded as
// launch targets for llama-server.
//
// This answers a question the setup flow otherwise leaves hanging: llama.cpp
// needs a `-m /path/to/model.gguf`, and someone who has been using Ollama
// already has several gigabytes of perfectly good GGUFs on disk. They are just
// stored content-addressed, with no extension and no name, so nobody finds them
// by looking. llama.cpp reads GGUF by magic bytes rather than filename, so the
// blob path works directly — no copy, no conversion, no second download.
func suggestOllamaWeightsForLlamaCpp(serverInstalled bool) {
	models, err := ollama.LocalGGUFs()
	if err != nil || len(models) == 0 {
		return
	}

	fmt.Println()
	uiIdle("already on disk", fmt.Sprintf("%d GGUF model(s) pulled by Ollama", len(models)))
	if !serverInstalled {
		// Without the binary this is context for later, not a command to run
		// now. Presenting it as the latter is how a user ends up pasting a
		// "command not found".
		uiDetail("Once llama-server is installed it can serve them directly — same files, no copy or conversion:")
	} else {
		uiDetail("llama.cpp can serve them directly — same files, no copy or conversion:")
	}

	const shown = 5
	for i, m := range models {
		if i == shown {
			uiDetail(fmt.Sprintf("… and %d more", len(models)-shown))
			break
		}
		fmt.Println(shell.KV(m.Name, shell.Muted(fmt.Sprintf("%.1f GB", m.SizeGB())),
			shell.KVWidth(m.Name)))
		fmt.Println(shell.StepCommand(fmt.Sprintf("llama-server -m %s --port 8080", m.Path)))
	}
	// Accuracy matters here: Ollama listens on 11434 and llama-server on 8080, so
	// they do NOT collide by default — an earlier version of this line claimed
	// they did. The real cost of running both is memory, since each loads its own
	// copy of the weights.
	uiDetail("Ollama (11434) and llama-server (8080) run side by side, but each loads " +
		"its own copy of the weights — stop one if RAM is tight. whisper.cpp also " +
		"defaults to 8080; /doctor reports that clash.")
}

// suggestOllamaInstead points at a working Ollama when llama.cpp is not
// installed.
//
// llama.cpp earns its place on hardware Ollama cannot serve (see
// docs/local_runtimes.md); it is not the easier option anywhere else. When the
// user has picked it, does not have it, and has a healthy Ollama already, saying
// so is more useful than walking them through a build they may not need.
func suggestOllamaInstead(llamaInstalled bool) {
	if llamaInstalled {
		return // they have it; the choice is theirs and it is a valid one
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := ollama.NewClient()
	if err := client.Health(ctx); err != nil {
		return // no working alternative to offer
	}

	fmt.Println()
	uiOK("ollama", "is running here and does the same job")
	if models, err := client.ListModels(ctx); err == nil && len(models) > 0 {
		uiDetail("it already has: " + strings.Join(modelIDs(models, 4), ", "))
	}
	uiDetail("Answer N here and choose \"Ollama (local)\" instead — no build required.")
	uiDetail("llama.cpp is for hardware Ollama cannot serve; see docs/local_runtimes.md.")
}

// modelIDs renders up to max model names for a one-line summary.
func modelIDs(models []providers.ModelInfo, max int) []string {
	out := make([]string, 0, max+1)
	for i, m := range models {
		if i == max {
			out = append(out, fmt.Sprintf("and %d more", len(models)-max))
			break
		}
		out = append(out, m.ID)
	}
	return out
}

// offerLlamaCppInstall installs llama.cpp when there is one unambiguous command
// for it, returning whether the binary is present afterwards.
//
// The narrowness is deliberate (see llamacpp.InstallCommand): a Homebrew bottle
// is a prebuilt binary and a single command, whereas building llama.cpp means
// choosing a GPU backend, which is the user's decision and not Helix's.
func offerLlamaCppInstall() bool {
	cmdLine, ok := llamacpp.InstallCommand()
	if !ok {
		// Instructions were already printed by Diagnose; nothing to offer.
		return false
	}

	fmt.Println()
	uiIdle("not installed", "Helix can install it")
	fmt.Println(shell.StepCommand(cmdLine))
	if !commands.AskForConfirmation("Run that now?") {
		uiIdle("skipped", "run it yourself and re-select llama.cpp when done")
		return false
	}

	// No spinner here, deliberately. The package manager writes its own progress
	// to this terminal, and animating over it produced interleaved garbage —
	// "Downloaded 12.4KB/ 12.4KBe llama.cpp (0.2.0)" — where the spinner
	// overwrote brew's line. Two writers, one cursor. brew's output is better
	// than anything Helix would draw, so let the child own the terminal.
	fmt.Println()
	fmt.Println(shell.StepCommand(cmdLine))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	unreg := utils.RegisterOperation(cancel)
	defer unreg()

	if err := runShellInstall(ctx, cmdLine); err != nil {
		fmt.Println()
		if ctx.Err() != nil {
			uiIdle("cancelled", "nothing was installed")
		} else {
			uiFail("install", err.Error())
		}
		uiUsage(cmdLine)
		return false
	}
	fmt.Println()

	// Trust the check, not the exit code: a package manager can succeed while
	// putting the binary somewhere this process cannot see (a PATH that does not
	// include the keg until a new shell).
	path, present := llamacpp.ServerInstalled()
	if !present {
		uiWarn("not on PATH", "the install reported success but llama-server is still missing")
		uiDetail("Open a new shell, or add Homebrew's bin directory to PATH.")
		return false
	}
	uiOK("installed", path)
	return true
}

// runShellInstall executes an install command through the shell.
func runShellInstall(ctx context.Context, cmdLine string) error {
	fields := strings.Fields(cmdLine)
	if len(fields) == 0 {
		return fmt.Errorf("empty install command")
	}
	// Executed as argv, never through a shell: the command is a constant from
	// llamacpp.InstallCommand, and running it via `sh -c` would add an
	// interpreter for no benefit.
	c := exec.CommandContext(ctx, fields[0], fields[1:]...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// guideLlamaCppModel walks an installed-but-idle llama-server to a running one.
//
// The order answers "does llama.cpp pull models like Ollama?" in the most useful
// way: it lists what is ALREADY on disk first (including Ollama's own blobs,
// which llama.cpp reads directly), and only then offers a download. It does pull
// — `-hf` fetches from Hugging Face and caches it — so there is never a reason
// to install a second runtime just to obtain weights.
func guideLlamaCppModel(endpoint string) {
	port := "8080"
	if u, err := url.Parse(endpoint); err == nil && u.Port() != "" {
		port = u.Port()
	}

	cached := llamacpp.CachedModels()
	ollamaModels, _ := ollama.LocalGGUFs()
	warnIfOllamaCannotSeeItsModels(ollamaModels)

	if len(cached) == 0 && len(ollamaModels) == 0 {
		fmt.Println()
		uiIdle("no GGUF models", "none on this machine yet")
		uiDetail("llama.cpp downloads them itself — no other tool needed:")
		printLlamaCppPullOptions(port)
		return
	}

	fmt.Println()
	uiOK("models on disk", "start llama-server with one of these")

	const shown = 4
	printed := 0
	for _, m := range cached {
		if printed == shown {
			break
		}
		fmt.Println(shell.KV(truncStr(m.Name, 34),
			shell.Muted(fmt.Sprintf("%.1f GB  ·  llama.cpp cache", m.SizeGB())),
			shell.KVWidth(truncStr(m.Name, 34))))
		fmt.Println(shell.StepCommand(llamacpp.ServeCommand(m.Path, port)))
		printed++
	}
	for _, m := range ollamaModels {
		if printed == shown {
			break
		}
		fmt.Println(shell.KV(truncStr(m.Name, 34),
			shell.Muted(fmt.Sprintf("%.1f GB  ·  pulled by Ollama", m.SizeGB())),
			shell.KVWidth(truncStr(m.Name, 34))))
		fmt.Println(shell.StepCommand(llamacpp.ServeCommand(m.Path, port)))
		printed++
	}
	if total := len(cached) + len(ollamaModels); total > printed {
		uiDetail(fmt.Sprintf("… and %d more", total-printed))
	}

	if len(ollamaModels) > 0 {
		uiDetail("Ollama's GGUFs are plain files llama.cpp can open directly — but not " +
			"every one will LOAD: Ollama converts some models with tensor layouts a given " +
			"llama.cpp build does not implement yet. If a load fails, the error and the log " +
			"tail are shown, and a -hf download below is the way past it.")
	}

	// Offer to run it. The wizard has already installed the binary and found the
	// model; stopping here to make the user copy a command back in is the step
	// that made this flow feel unfinished — Ollama's path both installs and
	// starts, and there is no reason this one should not.
	candidates := append(append([]launchChoice(nil),
		toLaunchChoices(cached, "llama.cpp cache")...),
		ollamaLaunchChoices(ollamaModels)...)
	if offerLlamaCppStart(candidates, endpoint, port) {
		return
	}

	fmt.Println()
	uiDetail("Or download a different one:")
	printLlamaCppPullOptions(port)
}

// launchChoice is one startable model.
type launchChoice struct {
	Label  string
	Path   string
	SizeGB float64
	Origin string
}

func toLaunchChoices(models []llamacpp.CachedModel, origin string) []launchChoice {
	out := make([]launchChoice, 0, len(models))
	for _, m := range models {
		out = append(out, launchChoice{Label: m.Name, Path: m.Path, SizeGB: m.SizeGB(), Origin: origin})
	}
	return out
}

func ollamaLaunchChoices(models []ollama.LocalModel) []launchChoice {
	out := make([]launchChoice, 0, len(models))
	for _, m := range models {
		out = append(out, launchChoice{
			Label: m.Name, Path: m.Path, SizeGB: m.SizeGB(), Origin: "pulled by Ollama",
		})
	}
	return out
}

// offerLlamaCppStart offers to launch llama-server on one of the models found,
// reporting whether a server is now answering.
func offerLlamaCppStart(choices []launchChoice, endpoint, port string) bool {
	if len(choices) == 0 {
		return false
	}

	fmt.Println()
	if !commands.AskForConfirmation("Start llama-server on one of these now?") {
		return false
	}

	choice := choices[0]
	if len(choices) > 1 {
		fmt.Println()
		for i, c := range choices {
			fmt.Printf("  %d) %-34s %5.1f GB  (%s)\n", i+1, truncStr(c.Label, 34), c.SizeGB, c.Origin)
		}
		answer := strings.TrimSpace(commands.AskLine(
			fmt.Sprintf("Which model? (1-%d, blank for 1)", len(choices))))
		if answer != "" {
			n, err := strconv.Atoi(answer)
			if err != nil || n < 1 || n > len(choices) {
				uiIdle("not a listed number", "nothing was started")
				return false
			}
			choice = choices[n-1]
		}
	}

	logPath, err := llamacpp.DefaultLogPath()
	if err != nil {
		uiFail("log file", err.Error())
		return false
	}

	srv, err := llamacpp.Start(llamacpp.StartOptions{
		ModelPath: choice.Path, Port: port, LogPath: logPath,
	})
	if err != nil {
		uiFail("llama-server", err.Error())
		return false
	}
	uiIdle("starting", fmt.Sprintf("llama-server pid %d, loading %s", srv.PID, choice.Label))

	if !waitForLlamaCpp(srv, endpoint, choice.SizeGB) {
		return false
	}

	uiOK("llama-server", "answering on "+port)
	// The process is detached and outlives this session, which the user has to
	// be told: they now own a process holding several gigabytes of RAM.
	w := shell.KVWidth("KEEP-ALIVE", "LOG")
	fmt.Println(shell.KV("KEEP-ALIVE", shell.Muted("runs after Helix exits · stop with  ")+
		shell.Value(srv.StopHint()), w))
	fmt.Println(shell.KV("LOG", shell.Value(srv.LogPath), w))

	reportResolvedLocalModel()
	return true
}

// waitForLlamaCpp waits for readiness, distinguishing a slow load from a failed
// one.
func waitForLlamaCpp(srv llamacpp.Server, endpoint string, sizeGB float64) bool {
	// Weights are memory-mapped at roughly disk speed, so the budget scales with
	// the model. A generous floor covers small models on slow disks.
	budget := time.Duration(30+int(sizeGB*20)) * time.Second
	if budget < 60*time.Second {
		budget = 60 * time.Second
	}

	err := runCancellableProgressWithTimeout(
		"LOADING MODEL",
		budget,
		func(ctx context.Context, progress func(string, int64, int64)) error {
			progress("LOADING MODEL", 0, 0)
			return llamacpp.WaitReady(ctx, endpoint, func(probeCtx context.Context) error {
				// A process that exited during load will never answer, so stop
				// waiting out the whole budget for it.
				if !srv.Alive() {
					return errServerExited
				}
				p, gerr := ai.GetProviderByName(llamacpp.Name)
				if gerr != nil {
					return gerr
				}
				return p.HealthCheck(probeCtx)
			})
		},
	)
	if err == nil {
		return true
	}

	if errors.Is(err, errServerExited) || !srv.Alive() {
		uiFail("llama-server", "exited while loading the model")
		if exitErr := srv.ExitError(); exitErr != nil {
			uiDetail(exitErr.Error())
		}
	} else {
		uiFail("llama-server", "did not become ready within "+budget.String())
		uiDetail("It may still be loading; /provider-status checks again.")
	}
	if tail := llamacpp.LogTail(srv.LogPath, 8); len(tail) > 0 {
		uiDetail("last lines of " + srv.LogPath + ":")
		for _, line := range tail {
			uiDetail(truncStr(line, 160))
		}
	}
	return false
}

// errServerExited stops the readiness wait early when the process is gone.
var errServerExited = errors.New("llama-server exited")

// printLlamaCppPullOptions lists -hf download commands suited to this hardware.
func printLlamaCppPullOptions(port string) {
	hw := providers.DetectHardware()
	var offered int
	for _, rec := range providers.RecommendLocalModels(hw) {
		if rec.Runtime != "llamacpp" || rec.HFRepo == "" {
			continue
		}
		fmt.Println(shell.KV(rec.DisplayName, "", shell.KVWidth(rec.DisplayName)))
		fmt.Println(shell.StepCommand(llamacpp.PullCommand(rec.HFRepo, port)))
		offered++
	}
	if offered == 0 {
		// RecommendLocalModels filters on RAM, so a very small machine can end
		// up with nothing. Say that rather than printing an empty list.
		uiIdle("no fit", fmt.Sprintf("no recommended model fits %d GB of RAM", hw.RAMGB))
		uiDetail("Browse GGUFs at https://huggingface.co/models?library=gguf and use:")
		fmt.Println(shell.StepCommand(llamacpp.PullCommand("<org>/<repo>", port)))
		return
	}
	uiDetail("The download is cached, so later launches reuse it.")
}

// warnIfOllamaCannotSeeItsmodels reports models present on disk that the RUNNING
// Ollama server does not list.
//
// The two can disagree, and when they do nothing else explains it. A server
// started with a different HOME (or OLLAMA_MODELS) reads a different store, so
// `ollama list` comes back empty while several gigabytes sit in ~/.ollama —
// and every request for those models answers 404, including Helix's own
// cloud-to-local fallback. Comparing the filesystem against the API turns that
// into one line instead of an afternoon.
func warnIfOllamaCannotSeeItsModels(onDisk []ollama.LocalModel) {
	if len(onDisk) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client := ollama.NewClient()
	if err := client.Health(ctx); err != nil {
		return // not running: a different situation, already reported elsewhere
	}
	served, err := client.ListModels(ctx)
	if err != nil {
		return
	}

	known := make(map[string]bool, len(served))
	for _, m := range served {
		known[m.ID] = true
	}
	var invisible []string
	for _, m := range onDisk {
		if !known[m.Name] {
			invisible = append(invisible, m.Name)
		}
	}
	if len(invisible) == 0 {
		return
	}

	fmt.Println()
	uiFail("ollama", fmt.Sprintf("does not list %d model(s) that are on disk", len(invisible)))
	uiDetail(strings.Join(invisible, ", "))
	uiDetail("The server is reading a DIFFERENT model store than ~/.ollama — usually " +
		"because it was started with another HOME or OLLAMA_MODELS. Those models will " +
		"404, including for Helix's own local fallback.")
	uiDetail("Find the server and what it inherited:")
	fmt.Println(shell.StepCommand("lsof -nP -iTCP:11434 -sTCP:LISTEN"))
	fmt.Println(shell.StepCommand("ps eww -p <pid> | tr ' ' '\\n' | grep -E 'HOME=|OLLAMA_MODELS='"))
	uiDetail("Restart it from your own shell to serve ~/.ollama again.")
}

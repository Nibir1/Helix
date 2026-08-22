// cmd/helix/main.go
// Purpose: Helix entrypoint — interactive REPL, non-interactive bridge,
// provider bootstrap, and the `helix update` subcommand.
// Hardening: process-wide SIGINT routing so Ctrl+C cancels the running
// operation instead of killing the shell; Ctrl+C at the prompt behaves like
// a real shell (fresh prompt, never exits).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/confinement"
	"helix/internal/daemon"
	"helix/internal/diagnostics"
	"helix/internal/hooks"
	"helix/internal/input"
	"helix/internal/providers"
	"helix/internal/rag"
	"helix/internal/recon"
	"helix/internal/session"
	"helix/internal/shell"
	"helix/internal/speech"
	"helix/internal/stealth"
	"helix/internal/utils"
	"helix/internal/ux"
	"helix/internal/vision"

	"github.com/fatih/color"
)

var (
	cfg         *config.Config
	env         shell.Env
	execConfig  commands.ExecuteConfig
	gitManager  *commands.GitManager
	sandbox     *commands.DirectorySandbox
	ragSystem   *rag.RAGSystem
	agentCore   *agent.Agent
	highlighter *utils.SyntaxHighlighter
)

func main() {
	// Landlock re-exec child (Linux only). Confines itself with the
	// kernel LSM, then runs the requested shell command.
	if len(os.Args) > 1 && os.Args[1] == "--confined-child" {
		os.Exit(confinement.RunConfinedChild(os.Args[2:]))
	}

	// telemetry-free crash diagnostics. Installed FIRST so every
	// later panic or fatal signal leaves a local, redacted report.
	diagnostics.Version = config.HelixVersion
	diagnostics.Install()
	defer diagnostics.RecoverMain() // first defer => runs last on panic unwind
	diagnostics.SelftestPanicIfRequested()

	// FIX (command-not-found): When Helix is the login shell or launched
	// from the GUI, the process inherits a minimal PATH and no profile files
	// ever run — commands like `code`, `brew`, or nvm-managed binaries then
	// fail with "command not found". Overlay the user's real login-shell
	// environment before anything executes commands.
	shell.ApplyLoginEnvironment()

	if len(os.Args) > 1 && os.Args[1] == "update" {
		runKnowledgeUpdate()
		return
	}
	// BlackBox Phase 4: `helix daemon` / `helix remote ...` /
	// `helix daemon install|uninstall|status`.
	if handled, code := runDaemonCommand(os.Args[1:]); handled {
		os.Exit(code)
	}
	if handled, code := maybeRunNonInteractive(); handled {
		os.Exit(code)
	}

	// FIX (interrupt hardening): Ctrl+C during any cooked-mode operation must
	// cancel the operation, not kill the shell. Installed before any
	// long-running work can start.
	utils.InstallInterruptHandler()

	var err error
	cfg, err = config.DefaultConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		return
	}
	// (startup sync stays: config → env, so early boot logs honor the preference)
	if cfg.UserPrefs.DebugMode {
		_ = os.Setenv("HELIX_DEBUG", "1")
	}
	if cfg.UserPrefs.UserName != "" {
		shell.SetUserName(cfg.UserPrefs.UserName)
	}
	env = shell.DetectEnvironment()
	sandbox = commands.NewDirectorySandbox()
	execConfig = commands.DefaultExecuteConfig()
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		homeDir = "/tmp"
	}
	db, err := rag.OpenDB(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Database error: %v\n", err)
		return
	}
	defer func() { _ = db.Close() }()
	_ = ai.InitProviders(ai.ProviderSettings{
		Provider:        normalizeProviderName(cfg.Provider),
		Model:           cfg.ProviderModel,
		CustomBaseURL:   cfg.CustomProviderBaseURL,
		LlamaCppBaseURL: cfg.LLM.LlamaCppURL,
	})
	// A first run is not finished when the provider is chosen — the packages
	// and the speech chain still have to happen, and they sit on the far side
	// of speech.Init. Remember it here, act on it there.
	needsSetup := cfg.Provider == "" || !ai.ModelIsLoaded()
	firstRun := needsSetup
	if !needsSetup {
		_ = ai.UseProvider(cfg.Provider)
		ai.UseModel(cfg.ProviderModel)
		// A saved placeholder ("local-gguf") is a label, not a model: while it
		// is active Helix assumes an 8k context and no vision whatever the
		// runtime actually loaded. Ask the runtime once, in the background, so
		// startup is not blocked on an unreachable sidecar.
		go func() {
			defer diagnostics.Guard("local-model-resolve")()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if resolved, changed := ai.ResolveActiveLocalModel(ctx); changed {
				cfg.ProviderModel = resolved
				_ = cfg.SavePreferences()
			}
		}()
	} else {
		if err := runNativeSetup(); err != nil {
			color.Red("Setup failed: %v", err)
			return
		}
		cfg.Provider = ai.ActiveProviderName()
		cfg.ProviderModel = ai.ActiveModel()
		_ = cfg.SavePreferences()
	}
	ragSystem = rag.NewSystem(env, db)
	ragSystem.SetSilent(true)
	go func() {
		defer diagnostics.Guard("rag-bootstrap")() // Guard returns the recovery closure; it must be CALLED
		_ = ragSystem.Initialize()
		// FIX: Use the unified debug toggle instead of reading the env var directly.
		if err := rag.KnowledgeBootstrap(context.Background(), db); err != nil &&
			utils.IsDebugMode() {
			fmt.Fprintf(os.Stderr, "[boot] knowledge bootstrap: %v\n", err)
		}
	}()

	// BlackBox Phase 1: speech engine (STT/TTS registry with failover). A
	// startup failure is non-fatal — text Helix keeps working; /blackbox setup
	// or /blackbox status diagnose.
	if err := speech.Init(speechConfigFrom(cfg.Speech)); err != nil {
		color.Yellow("Speech engine unavailable: %v", err)
	}
	if firstRun {
		// The rest of first run: system packages, then the speech chain. A
		// precompiled binary's user has no other way to learn that speaking
		// needs sox and seeing needs ffmpeg.
		runFirstRunStages()
	}
	gui := ux.NewUX()
	// Sync the global typewriter preference with the UX layer
	gui.SetTypewriteAll(cfg.UserPrefs.TypewriteAll)

	dbg := utils.IsDebugMode()
	audioDone := make(chan error, 1)
	go func() {
		defer diagnostics.Guard("audio-init")() // Guard returns the recovery closure; it must be CALLED
		audioDone <- audio.Init()
	}()
	select {
	case aerr := <-audioDone:
		if aerr != nil {
			color.Yellow("Audio engine unavailable: %v", aerr)
			color.Yellow("Helix will stay silent. Try /audio on after checking your sound device.")
			if dbg {
				color.Yellow("Audio debug: speaker initialization failed at startup.")
			}
		}
	case <-time.After(2 * time.Second):
		color.Yellow("Audio engine init timeout; continuing silent")
		color.Yellow("Use /audio on to retry once your sound device is available.")
		if dbg {
			color.Yellow("Audio debug: startup initialization exceeded 2s.")
		}
	}
	commands.SetPrompter(gui)
	highlighter = utils.NewSyntaxHighlighter()
	commands.SetSyntaxHighlighter(highlighter)
	stealthExec := stealth.NewStealthExecutor(stealth.DefaultStealthConfig())
	reconEng := recon.NewReconEngine(env, recon.DefaultReconConfig())
	agentCore = agent.NewAgent(env, ragSystem, sandbox, execConfig, cfg.UserPrefs.TypingEffect, gui, stealthExec, reconEng)
	// /git shares the AGENT's git manager. This package used to declare its own
	// and never assign it, so /git ran against a nil receiver carrying a
	// zero-valued ExecuteConfig — SafeMode false, so the pre-execution safety
	// check was skipped, and env.Shell empty, so commands ran under the wrong
	// interpreter. One instance, correctly configured, removes both.
	gitManager = agentCore.GitManager()
	agentCore.Slash = agent.SlashFunc(handleSlashCommand)
	agentCore.Agentic = cfg.UserPrefs.AgenticMode
	histPath := homeDir + "/.helix_history"
	history, _ := utils.LoadHistory(histPath)

	// BlackBox Phase 2: voice channel wiring — prompter swap, persisted mode,
	// and the spoken-response seam.
	initVoiceMode()
	agentCore.OnSpeak = func(text string) {
		if !speech.TTSEnabled() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		// SpeakStream begins playback after the first sentence synthesizes
		// (one-ahead pipelining) instead of waiting for the whole reply.
		if err := speech.SpeakStream(ctx, text); err != nil && utils.IsDebugMode() {
			fmt.Fprintf(os.Stderr, "[voice] speak: %v\n", err)
		}
	}

	// BlackBox Phase 11: arm the cloud→local brain failover. The interactive
	// shell has no connectivity monitor (that is the daemon's job), so here the
	// circuit breaker in internal/ai does the detecting: repeated availability
	// failures trip it, and a half-open probe restores the cloud model later.
	// A misconfigured fallback only warns — running unprotected is strictly
	// better than refusing to start.
	if err := ai.ConfigureLocalFallback(cfg.AIFallback()); err != nil {
		color.Yellow("Local LLM fallback disabled: %v", err)
	}
	ai.SetFailoverNotice(func(msg string) {
		// Printed as well as spoken: a silent brain swap would look like the
		// model simply got worse. The user must be able to see it too when TTS
		// is off or unavailable.
		color.Yellow("[llm] %s", msg)
		if agentCore.OnSpeak != nil {
			agentCore.OnSpeak(msg)
		}
	})

	// BlackBox Phase 5: opt-in camera vision seams (memory-only frames).
	visionSvc = vision.NewCaptureService()
	agentCore.VisionEnabled = func() bool {
		if !cfg.Vision.Enabled {
			return false
		}
		if cfg.Vision.Provider != "" {
			return ai.ProviderVisionCapable(cfg.Vision.Provider)
		}
		return ai.VisionCapable()
	}
	agentCore.VisionCapture = func(ctx context.Context) ([]byte, error) {
		frame, err := visionSvc.CaptureFrame(ctx)
		if err != nil {
			return nil, err
		}
		return frame.Data, nil
	}
	agentCore.VisionCall = func(prompt string, image []byte) (string, error) {
		parts := []providers.MessagePart{{Type: providers.PartImage, ImageData: image}}
		var resp string
		var err error
		providerName := cfg.Vision.Provider
		if providerName == "" {
			providerName = ai.ActiveProviderName()
		}
		// vision.model lets frames go to a small fast VLM while chat keeps the
		// big model: the companion loop runs on a timer and shares the runtime
		// with the conversation, so the model that answers questions is usually
		// the wrong one to describe a frame every 20 seconds.
		resp, err = ai.RunVisionModelOn(prompt, parts, cfg.Vision.Provider, cfg.Vision.Model)
		journalVisionEvent("frame", providerName, 1)
		return resp, err
	}
	// §10 frame-to-insight metric: resolve the answering provider exactly as
	// VisionCall does, then log the sample to ~/.helix/metrics/vision.jsonl.
	agentCore.OnVisionMetric = func(metric string, latency time.Duration) {
		provider := cfg.Vision.Provider
		if provider == "" {
			provider = ai.ActiveProviderName()
		}
		logVisionLatency(metric, latency, provider)
	}

	fmt.Println("⚡ Helix Native Shell. Type '/help' for SOS or 'exit' to quit.")

	// BlackBox Phase 4B: stateful awareness for the interactive session too
	// (conversation memory + safe-subset undo journal).
	if sess, err := session.NewRingStore(session.DefaultCapacity); err == nil {
		agentCore.Session = sess
	}
	if undo, err := session.NewUndoJournal(); err == nil {
		agentCore.Undo = undo
	}

	// The harness's own state: the task list the planner can see, the local
	// policy hooks, the repository's project notes, and the approval posture.
	if todos, err := session.NewTodoList(); err == nil {
		todoList = todos
		agentCore.Todos = todos
	} else {
		// A corrupt task file must not take the shell down with it, but it must
		// not be silent either: the planner would then be planning against a
		// task list the user believes it can see.
		color.Yellow("Task list unavailable: %v", err)
		color.Yellow("/todo is disabled this session; fix or delete ~/.helix/todo.json.")
	}
	if hookSet, err := hooks.Load(); err == nil {
		agentCore.Hooks = hookSet
	} else {
		// Failing closed on load means NO hooks run — a user who added a
		// blocking hook must hear that it is not guarding them.
		color.Red("Hook configuration failed to load: %v", err)
		color.Red("NO hooks are active this session. Run /hooks to see the config path.")
	}
	agentCore.ProjectContext = loadProjectContext
	applyPersistedPermission()
	if cfg.UserPrefs.AgenticSteps > 0 {
		agentCore.MaxAgenticSteps = cfg.UserPrefs.AgenticSteps
	}
	if _, path, ok := loadProjectContext(); ok {
		color.Cyan("Project context loaded from %s", path)
	}

	fireSessionHook(hooks.SessionStart)
	defer fireSessionHook(hooks.SessionEnd)

	// §10 E2E voice-command latency (wake→execution start, ≤6s local): set
	// when a wake event fires, measured at dispatch of the next voice turn.
	var lastWakeAt time.Time

	for {
		// TTY heartbeat: the daemon's voice loop yields the microphone to
		// the foreground session while this lock stays fresh.
		daemon.Heartbeat()

		var ev input.InputEvent
		if voiceModeActive {
			var verr error
			ev, verr = voiceTurnWithRetry()
			if verr != nil {
				if verr == errVoiceStopped {
					continue // kill phrase: mode line already announced it
				}
				if errors.Is(verr, errVoiceHandled) {
					continue // spoken command already ran and answered
				}
				// Graceful degradation: mic/STT trouble must never brick the
				// shell — offer one typed turn while staying in voice mode.
				// The error is often multi-line (a provider chain failure
				// carries the address it dialled and the command that starts
				// it). Appending an instruction to the end of that glued
				// "— type /blackbox off" onto the tail of a shell command.
				fmt.Println(shell.PanelLine(shell.Badge(shell.StateBad, "voice unavailable")))
				for _, line := range strings.Split(strings.TrimSpace(verr.Error()), "\n") {
					fmt.Println(shell.PanelLine(shell.Muted(strings.TrimRight(line, " "))))
				}
				fmt.Println(shell.Hint("/blackbox off returns to the keyboard  ·  /blackbox status diagnoses"))
				line, rerr := shell.ReadLine(shell.GetContext(), highlighter, history)
				if rerr != nil {
					if rerr.Error() == "EOF" {
						break
					}
					continue
				}
				ev = input.InputEvent{Text: strings.TrimSpace(line), Channel: input.ChannelText}
			}
		} else {
			line, err := shell.ReadLine(shell.GetContext(), highlighter, history)
			if err != nil {
				if err.Error() == "EOF" {
					break // Ctrl+D on empty line / closed stdin still exits.
				}
				// FIX (interrupt hardening): Ctrl+C at the prompt behaves like a
				// real shell: clear the line and redraw a fresh prompt. It must
				// NEVER exit Helix.
				continue
			}
			ev = input.InputEvent{Text: strings.TrimSpace(line), Channel: input.ChannelText}
		}
		if ev.Text == "" {
			continue
		}
		if ev.Text == "exit" || ev.Text == "quit" {
			break
		}
		if agentCore.PersistsHistory() {
			_ = utils.AppendHistory(histPath, ev.Text)
		}
		// In-memory history always records the line (ghost-text suggestions
		// keep working); stealth MemoryOnly only suppresses the on-disk file.
		history = append(history, ev.Text)
		shell.PrintTransient(ev.Text)
		if ev.Channel == input.ChannelVoice && !lastWakeAt.IsZero() {
			logVoiceLatency("wake_to_exec", time.Since(lastWakeAt), ev.Meta)
			lastWakeAt = time.Time{}
		}
		agentCore.HandleInputEvent(ev)
		// Derived from state the subsystems already recorded, never from a probe:
		// this is the hot loop. An unconditional CLEAR here used to claim the
		// grid was fine while the STT chain was falling back to a sidecar that
		// was not running.
		if status := evaluateGridStatus(currentGridSignals()); status.Degraded {
			gui.PrintWarning(status.Line)
		} else {
			gui.PrintSuccess(status.Line)
		}
		fmt.Print("\x1b]133;D;0\x07")

		// BlackBox Phase 3 hands-free: after a completed turn, hold in
		// wake-only listening (no transcription) until a wake event fires or
		// the idle window expires; then the next loop iteration runs another
		// voice turn. Disabled wake config = classic push-to-talk per turn.
		if voiceModeActive {
			// The microphone is provably closed here — the turn finished and the
			// next capture has not started — so this is one of the two points
			// where Helix may say something it was not asked for.
			drainCompanion()

			for {
				wakeEv, outcome := wakeListenUntilArmed()
				if outcome == wakeCompanionSpoke {
					// The scanner stopped on the way out of the listen, so the
					// remark is spoken into a closed microphone; then listening
					// resumes rather than falling through to open capture.
					drainCompanion()
					continue
				}
				if outcome == wakeFired {
					lastWakeAt = wakeEv.DetectedAt
				} else {
					// Wake gating lapsing back to open capture is a change the
					// user asked for wake for — say so once instead of silently
					// listening.
					noteWakeLapse(outcome)
				}
				break
			}
		}
	}
}

func runNativeSetup() error {
	fmt.Println(shell.PanelTitle("neural link"))
	fmt.Println(shell.PanelLine(shell.Muted("choose the model that will think for Helix")))
	fmt.Println(shell.PanelGap())

	items := make([]shell.MenuItem, 0, len(providerOptions))
	for _, p := range providerOptions {
		items = append(items, shell.MenuItem{
			Label: p.Label,
			Note:  providerNote(p.ID),
			Tag:   providerTag(p.ID),
			Good:  ai.ProviderHasSavedKey(p.ID) || p.ID == "ollama",
		})
	}
	for _, l := range shell.Menu(items) {
		fmt.Println(l)
	}
	printAdvancedProviders()
	fmt.Println(shell.PanelEnd())
	choiceStr := commands.AskLine(shell.Prompt("provider number", ""))
	var choice int
	if _, err := fmt.Sscanf(choiceStr, "%d", &choice); err != nil {
		return fmt.Errorf("invalid selection: %w", err)
	}
	if choice < 1 || choice > len(providerOptions) {
		return fmt.Errorf("invalid selection")
	}
	prov := providerOptions[choice-1].ID
	if err := useProviderInteractive(prov); err != nil {
		return err
	}
	return nil
}

// printAdvancedProviders names what the first-run menu leaves out.
//
// The menu is the shortest path to a working shell, so it lists the providers
// that need only a key or a running Ollama. Anything requiring a hand-managed
// runtime is reachable afterwards instead — but only if the user is told it
// exists, which is the difference between a curated menu and a hidden feature.
func printAdvancedProviders() {
	extra := advancedProviderNames()
	if len(extra) == 0 {
		return
	}
	fmt.Println(shell.PanelGap())
	fmt.Println(shell.PanelLine(shell.Muted("also available after setup: " + strings.Join(extra, ", "))))
	fmt.Println(shell.Hint("/provider use <name>  ·  see docs/local_runtimes.md"))
}

// providerNote says what picking a provider commits you to, in the moment the
// choice is made. A bare vendor list makes the reader supply that from memory.
func providerNote(id string) string {
	if id == "ollama" {
		return "runs on this machine  ·  no key, no per-call cost"
	}
	return "cloud  ·  API key required"
}

// providerTag marks the ones the user can pick without further work.
func providerTag(id string) string {
	if ai.ProviderHasSavedKey(id) {
		return "key saved"
	}
	if id == "ollama" {
		return "local"
	}
	return ""
}

// advancedProviderNames returns registered providers absent from the first-run
// menu, so the two can never silently drift apart.
func advancedProviderNames() []string {
	inMenu := make(map[string]bool, len(providerOptions))
	for _, p := range providerOptions {
		inMenu[p.ID] = true
	}
	var out []string
	for _, name := range ai.ListProviders() {
		if !inMenu[name] && name != "custom" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// runKnowledgeUpdate implements the `helix update` subcommand.
// FIX (interrupt hardening): the subcommand is cancellable via Ctrl+C and
// exits with 130 (standard SIGINT code) instead of dying mid-write.
func runKnowledgeUpdate() {
	color.Cyan("Helix Knowledge Update Tool")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}
	db, err := rag.OpenDB(homeDir)
	if err != nil {
		color.Red("Database error: %v", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	utils.InstallInterruptHandler()
	ctx, cancel := context.WithCancel(context.Background())
	unreg := utils.RegisterOperation(cancel)
	err = rag.UpdateAll(ctx, db, true)
	unreg()
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			color.Yellow("Update cancelled.")
			os.Exit(130)
		}
		if errors.Is(err, rag.ErrOffline) {
			color.Yellow("Offline — knowledge update requires internet connectivity.")
			os.Exit(1)
		}
		color.Red("Update failed: %v", err)
		os.Exit(1)
	}
	color.Green("Knowledge base updated successfully.")
}

// applyPersistedPermission restores the saved approval posture.
//
// It also migrates the old, dead `default_mode` preference: that field was
// written to config from the very first release and read by nothing, so a user
// who had set it was carrying a setting that never applied. Where it names a
// real mode, honor it once and record it under the key that now works.
func applyPersistedPermission() {
	saved := strings.TrimSpace(cfg.UserPrefs.Permission)
	migrated := false
	if saved == "" {
		saved = strings.TrimSpace(cfg.UserPrefs.DefaultMode)
		migrated = saved != ""
	}
	if saved == "" {
		return
	}
	mode, ok := agent.ParsePermissionMode(saved)
	if !ok {
		color.Yellow("Saved permission mode %q is not recognized; using the default (ask).", saved)
		return
	}
	agentCore.SetPermission(mode)
	if migrated {
		cfg.UserPrefs.Permission = string(mode)
		_ = cfg.SavePreferences()
	}
	if mode != agent.PermissionAsk {
		// Any posture other than the default changes what happens without
		// asking; starting a session in it silently would be a surprise.
		color.Yellow("Permission mode: %s — %s", mode, mode.Describe())
	}
}

// fireSessionHook runs the session-start / session-end hooks. Failures are
// reported by the agent's hook runner; a hook cannot abort the session, because
// there is no step here for it to deny.
func fireSessionHook(ev hooks.Event) {
	if agentCore == nil || agentCore.Hooks == nil {
		return
	}
	wd, _ := os.Getwd()
	agentCore.FireSessionHook(ev, wd)
}

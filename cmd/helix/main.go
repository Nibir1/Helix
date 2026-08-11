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
	"strings"
	"time"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/audio"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/confinement"
	"helix/internal/diagnostics"
	"helix/internal/rag"
	"helix/internal/recon"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/utils"
	"helix/internal/ux"

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

	if len(os.Args) > 1 && os.Args[1] == "update" {
		runKnowledgeUpdate()
		return
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
		Provider:      normalizeProviderName(cfg.Provider),
		Model:         cfg.ProviderModel,
		CustomBaseURL: cfg.CustomProviderBaseURL,
	})
	needsSetup := cfg.Provider == "" || !ai.ModelIsLoaded()
	if !needsSetup {
		_ = ai.UseProvider(cfg.Provider)
		ai.UseModel(cfg.ProviderModel)
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
		defer diagnostics.Guard("rag-bootstrap")
		_ = ragSystem.Initialize()
		// FIX: Use the unified debug toggle instead of reading the env var directly.
		if err := rag.KnowledgeBootstrap(context.Background(), db); err != nil &&
			utils.IsDebugMode() {
			fmt.Fprintf(os.Stderr, "[boot] knowledge bootstrap: %v\n", err)
		}
	}()
	gui := ux.NewUX()
	// Sync the global typewriter preference with the UX layer
	gui.SetTypewriteAll(cfg.UserPrefs.TypewriteAll)

	dbg := utils.IsDebugMode()
	audioDone := make(chan error, 1)
	go func() {
		defer diagnostics.Guard("audio-init")
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
	agentCore.OnSlashCommand = func(input string) bool { return handleSlashCommand(input) }
	histPath := homeDir + "/.helix_history"
	history, _ := utils.LoadHistory(histPath)
	fmt.Println("⚡ Helix Native Shell. Type '/help' for SOS or 'exit' to quit.")
	for {
		input, err := shell.ReadLine(shell.GetContext(), highlighter, history)
		if err != nil {
			if err.Error() == "EOF" {
				break // Ctrl+D on empty line / closed stdin still exits.
			}
			// FIX (interrupt hardening): Ctrl+C at the prompt behaves like a
			// real shell: clear the line and redraw a fresh prompt. It must
			// NEVER exit Helix.
			continue
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		_ = utils.AppendHistory(histPath, input)
		history = append(history, input)
		shell.PrintTransient(input)
		agentCore.HandleInput(input)
		gui.PrintSuccess("Helix :: GRID STATUS :: CLEAR")
		fmt.Print("\x1b]133;D;0\x07")
	}
}

func runNativeSetup() error {
	color.Cyan("⚡ HELIX NEURAL LINK CONFIGURATION")
	color.Cyan("Select AI Provider:")
	for i, p := range providerOptions {
		fmt.Printf("%d) %s\n", i+1, p.Label)
	}
	choiceStr := commands.AskLine("Enter provider number")
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

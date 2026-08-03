// cmd/helix/main.go
// Purpose: Entry point for Helix – AI-powered CLI assistant.
package main

import (
	"bufio"
	"os"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/rag"
	"helix/internal/recon"
	"helix/internal/shell"
	"helix/internal/stealth"
	"helix/internal/tui"
	"helix/internal/utils"
	"helix/internal/ux"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fatih/color"
)

var (
	cfg        *config.Config
	env        shell.Env
	online     bool
	execConfig commands.ExecuteConfig
	gitManager *commands.GitManager
	sandbox    *commands.DirectorySandbox
	ragSystem  *rag.RAGSystem
	agentCore  *agent.Agent
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runKnowledgeUpdate()
		return
	}

	color.Cyan("Helix v%s - AI-powered CLI Agent", config.HelixVersion)

	var err error

	cfg, err = config.DefaultConfig()
	if err != nil {
		color.Red("Config error: %v", err)
		return
	}

	env = shell.DetectEnvironment()

	caser := cases.Title(language.Und)
	color.Blue("%s (%s shell)", caser.String(env.OSName), env.Shell)

	online = utils.IsOnline(5 * time.Second)

	if online {
		color.Green("Online mode enabled")
	} else {
		color.Yellow("Offline mode - local providers recommended")
	}

	sandbox = commands.NewDirectorySandbox()
	execConfig = commands.DefaultExecuteConfig()

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	db, err := rag.OpenDB(homeDir)
	if err != nil {
		color.Red("Database error: %v", err)
		return
	}
	defer db.Close()

	color.Green("Knowledge database ready")

	if err := ai.InitProviders(ai.ProviderSettings{
		Provider:      normalizeProviderName(cfg.Provider),
		Model:         cfg.ProviderModel,
		CustomBaseURL: cfg.CustomProviderBaseURL,
	}); err != nil {
		color.Red("Provider initialization failed: %v", err)
		return
	}
	defer ai.StopLocalRuntimes()

	selectedProvider := ""

	for {
		selectedProvider = chooseProviderWithSaved(cfg)

		if isRemoteProvider(selectedProvider) && !online {
			color.Yellow("No internet detected for remote provider %q.", selectedProvider)

			if commands.AskForConfirmation("Continue anyway?") {
				break
			}

			continue
		}

		break
	}

	if err := setupProvider(selectedProvider); err != nil {
		color.Red("Provider setup failed: %v", err)
		runEnhancedMockMode()
		return
	}

	// FIX: Activate provider BEFORE selecting model so ListProviderModels works
	if err := ai.UseProvider(selectedProvider); err != nil {
		color.Red("Failed to activate provider: %v", err)
		runEnhancedMockMode()
		return
	}

	if err := selectModelForProvider(selectedProvider); err != nil {
		color.Red("Model selection failed: %v", err)
		runEnhancedMockMode()
		return
	}

	cfg.Provider = selectedProvider
	cfg.ProviderModel = ai.ActiveModel()

	if err := cfg.SavePreferences(); err != nil {
		color.Yellow("Could not save preferences: %v", err)
	}

	color.Green("AI provider ready: %s", selectedProvider)
	color.Green("AI model ready: %s", ai.ActiveModel())

	color.Blue("Initializing RAG system...")

	ragSystem = rag.NewSystem(env, db)

	if err := ragSystem.InitializeBlocking(); err != nil {
		color.Red("RAG initialization failed: %v", err)
	} else if ragSystem.IsInitialized() {
		color.Green("RAG system fully initialized")
	} else {
		color.Yellow("RAG system did not fully initialize; Helix will run without RAG.")
	}

	color.Blue("Running quick AI response test...")

	resp, err := ai.RunModel("hello")
	if err != nil || strings.TrimSpace(resp) == "" {
		color.Red("Basic AI test failed")
	} else {
		color.Green("AI operational")
	}

	color.Cyan("Connecting Neural Grid Interface...")

	tuiChan := make(chan tea.Msg, 100)

	gui := ux.NewUX()
	gui.SetEventHandler(func(evt interface{}) {
		tuiChan <- evt
	})

	commands.SetPrompter(gui)

	originalStdout := os.Stdout
	restoreStdio := utils.HijackStdio(tuiChan)
	defer restoreStdio()

	color.Output = os.Stdout

	stealthExec := stealth.NewStealthExecutor(stealth.DefaultStealthConfig())

	reconConfig := recon.DefaultReconConfig()
	reconEng := recon.NewReconEngine(env, reconConfig)

	agentCore = agent.NewAgent(
		env,
		ragSystem,
		sandbox,
		execConfig,
		cfg.UserPrefs.TypingEffect,
		gui,
		stealthExec,
		reconEng,
	)

	agentCore.OnSlashCommand = func(input string) bool {
		return handleSlashCommand(input)
	}

	if err := tui.Start(agentCore, tuiChan, originalStdout); err != nil {
		restoreStdio()
		color.Red("TUI Critical Failure: %v", err)
		os.Exit(1)
	}
}

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
	defer db.Close()

	if err := rag.UpdateAll(db); err != nil {
		color.Red("Update failed: %v", err)
		os.Exit(1)
	}

	color.Green("Knowledge base updated successfully.")
}

func runEnhancedMockMode() {
	color.Yellow("Mock mode enabled - no real AI")

	env = shell.DetectEnvironment()
	execConfig.DryRun = true

	reader := bufio.NewReader(os.Stdin)

	for {
		color.Cyan("[mock]› ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "/exit" {
			color.Green("Goodbye!")
			return
		}

		color.Yellow("Mock response: %s", input)
	}
}

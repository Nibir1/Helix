// cmd/helix/main.go
// Purpose: Entry point for Helix – AI‑powered CLI assistant.
// Author: Helix Red Team
// Date: 2026-05-09
// Dependencies: all internal packages, Bubble Tea, fatih/color

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
	// Standard CLI startup logs (visible before TUI takes over alt-screen)
	color.Cyan("Helix v%s - AI-powered CLI Agent", config.HelixVersion)

	// --------------------------
	// Load config
	// --------------------------
	var err error
	cfg, err = config.DefaultConfig()
	if err != nil {
		color.Red("Config error: %v", err)
		return
	}

	// --------------------------
	// Detect environment
	// --------------------------
	env = shell.DetectEnvironment()
	// Use cases.Title to avoid deprecated strings.Title
	caser := cases.Title(language.Und)
	color.Blue("%s (%s shell)", caser.String(env.OSName), env.Shell)

	// --------------------------
	// Online check
	// --------------------------
	online = utils.IsOnline(5 * time.Second)
	if online {
		color.Green("Online mode enabled")
	} else {
		color.Yellow("Offline mode - using local model")
	}

	// --------------------------
	// Sandbox + execution config
	// --------------------------
	sandbox = commands.NewDirectorySandbox()
	execConfig = commands.DefaultExecuteConfig()

	// --------------------------
	// AI Provider selection
	// --------------------------
	selectedProvider := askForAIProvider()

	if selectedProvider == ai.ProviderOpenAI && !online {
		color.Red("No internet - cannot use OpenAI provider.")
		color.Yellow("Switching to local model.")
		selectedProvider = ai.ProviderLocal
	}

	if selectedProvider == ai.ProviderOpenAI {
		if err := setupOpenAIProvider(); err != nil {
			color.Red("OpenAI setup failed: %v", err)
			color.Yellow("Switching to mock mode.")
			runEnhancedMockMode()
			return
		}
		ai.SetProvider(ai.ProviderOpenAI)
		color.Green("Using OpenAI cloud provider")
	} else {
		// Local model
		ai.SetProvider(ai.ProviderLocal)

		if err := cfg.EnsureModelDir(); err != nil {
			color.Red("Model dir error: %v", err)
			return
		}

		color.Blue("Checking local model…")
		if err := ai.DownloadModel(cfg.ModelFile, config.ModelURL, config.ModelChecksum); err != nil {
			color.Red("Model download failed: %v", err)
			runEnhancedMockMode()
			return
		}

		fileInfo, err := os.Stat(cfg.ModelFile)
		if err != nil {
			color.Red("Model missing: %v", err)
			runEnhancedMockMode()
			return
		}

		color.Green("Local model found (%.2f MB)", float64(fileInfo.Size())/(1024*1024))

		color.Blue("🔧 Loading model…")
		if err := ai.LoadModel(cfg.ModelFile); err != nil {
			color.Red("Failed to load local model: %v", err)
			runEnhancedMockMode()
			return
		}
		defer ai.CloseModel()

		color.Green("Local AI loaded successfully")
	}

	// --------------------------
	// Initialize RAG (blocking on first run)
	// --------------------------
	color.Blue("Initializing RAG system...")
	ragSystem = rag.NewSystem(env)

	if err := ragSystem.InitializeBlocking(); err != nil {
		color.Red("RAG initialization failed: %v", err)
	} else if ragSystem.IsInitialized() {
		color.Green("RAG system fully initialized")
	} else {
		color.Yellow("RAG system did not fully initialize; Helix will run without RAG.")
	}

	// --------------------------
	// Minimal sanity check
	// --------------------------
	color.Blue("Running quick AI response test...")
	resp, err := ai.RunModel("hello")
	if err != nil || strings.TrimSpace(resp) == "" {
		color.Red("Basic AI test failed")
	} else {
		color.Green("AI operational")
	}

	// --------------------------
	// TUI & Agent Initialization
	// --------------------------
	color.Cyan("Connecting Neural Grid Interface...")

	// 1. Create the TUI plumbing (carries generic tea.Msg events)
	tuiChan := make(chan tea.Msg, 100)

	// 2. Configure UX to use this channel (Headless/TUI mode)
	gui := ux.NewUX()
	gui.SetEventHandler(func(evt interface{}) {
		tuiChan <- evt
	})

	// 3. Save Original Output & Activate Hijacker
	originalStdout := os.Stdout

	// Hijack os.Stdout/Stderr → pipe → TUI
	restoreStdio := utils.HijackStdio(tuiChan)
	defer restoreStdio()

	// CRITICAL: Force color library to use the hijacked pipe
	color.Output = os.Stdout

	// --------------------------
	// Stealth + Recon Initialization (Phase 1)
	// --------------------------
	stealthExec := stealth.NewStealthExecutor(stealth.DefaultStealthConfig())
	reconConfig := recon.DefaultReconConfig()
	reconEng := recon.NewReconEngine(env, reconConfig)

	// 4. Initialize Agent with the TUI-aware UX, stealth, and recon
	agentCore = agent.NewAgent(
		env,
		ragSystem, // NOW: pass the real RAG system (not a placeholder)
		sandbox,
		execConfig,
		cfg.UserPrefs.TypingEffect,
		gui,         // TUI‑aware UX
		stealthExec, // memory‑only executor
		reconEng,    // multi‑tool recon orchestrator
	)

	// Register slash command handler
	agentCore.OnSlashCommand = func(input string) bool {
		return handleSlashCommand(input)
	}

	// 5. Launch The Grid (Bubble Tea Program)
	// Bubble Tea writes to the real terminal (originalStdout), bypassing the hijack.
	if err := tui.Start(agentCore, tuiChan, originalStdout); err != nil {
		restoreStdio()
		color.Red("TUI Critical Failure: %v", err)
		os.Exit(1)
	}
}

// -----------------------------------------------------------------------------
// MOCK MODE
// -----------------------------------------------------------------------------
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

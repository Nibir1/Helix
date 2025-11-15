package main

import (
	"bufio"
	"os"
	"strings"
	"time"

	"helix/internal/agent"
	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/config"
	"helix/internal/rag"
	"helix/internal/shell"
	"helix/internal/utils"
	"helix/internal/ux"

	"github.com/fatih/color"
)

// Package-level variables
var (
	cfg               *config.Config
	env               shell.Env
	pb                *ai.PromptBuilder
	online            bool
	execConfig        commands.ExecuteConfig
	gitManager        *commands.GitManager
	syntaxHighlighter *utils.SyntaxHighlighter
	sandbox           *commands.DirectorySandbox
	ragSystem         *rag.RAGSystem
	aiProvider        AIProvider   // Provider type (OpenAI/local)
	openAIAPIKey      string       // In-memory key
	agentCore         *agent.Agent // Agent Mode core
)

func main() {
	// Initialize color output
	color.Cyan("🚀 Helix v%s — AI & RAG-powered CLI Assistant", config.HelixVersion)
	color.Yellow("Repository: https://github.com/Nibir1/Helix")

	// Load configuration
	var err error
	cfg, err = config.DefaultConfig()
	if err != nil {
		color.Red("Error loading config: %v", err)
		return
	}

	// Detect environment
	env = shell.DetectEnvironment()
	color.Blue("🌍 Detected: %s (%s shell)", strings.Title(env.OSName), env.Shell)

	// Check internet connectivity
	online = utils.IsOnline(5 * time.Second)
	if online {
		color.Green("✅ Online mode - real-time capabilities available")
	} else {
		color.Yellow("⚠️  Offline mode - using local AI only")
	}

	// Initialize directory sandbox
	sandbox = commands.NewDirectorySandbox()

	// Default execution config
	execConfig = commands.DefaultExecuteConfig()

	// Git manager
	gitManager = commands.NewGitManager(env, execConfig, sandbox)

	// Syntax highlighter
	syntaxHighlighter = utils.NewSyntaxHighlighter()
	commands.SetSyntaxHighlighter(syntaxHighlighter)

	// Ask user which AI provider they want
	selectedProvider := askForAIProvider()

	// If user chooses OpenAI but offline → fallback
	if selectedProvider == ai.ProviderOpenAI && !online {
		color.Red("❌ No internet detected. OpenAI mode is unavailable.")
		color.Yellow("💡 Falling back to local model.")
		selectedProvider = ai.ProviderLocal
	}

	// Provider: OpenAI
	if selectedProvider == ai.ProviderOpenAI {
		if err := setupOpenAIProvider(); err != nil {
			color.Red("❌ Failed to configure OpenAI: %v", err)
			color.Yellow("Running in enhanced mock mode.")
			runEnhancedMockMode()
			return
		}
		ai.SetProvider(ai.ProviderOpenAI)
		color.Green("✅ Using OpenAI cloud provider")

	} else {
		// Provider: Local model
		ai.SetProvider(ai.ProviderLocal)

		if err := cfg.EnsureModelDir(); err != nil {
			color.Red("Error creating model directory: %v", err)
			return
		}

		color.Blue("📥 Checking for local AI model...")
		if err := ai.DownloadModel(cfg.ModelFile, config.ModelURL, config.ModelChecksum); err != nil {
			color.Red("❌ Model download error: %v", err)
			color.Yellow("Running in enhanced mock mode.")
			runEnhancedMockMode()
			return
		}

		fileInfo, err := os.Stat(cfg.ModelFile)
		if err != nil {
			color.Red("❌ Model file missing: %v", err)
			runEnhancedMockMode()
			return
		}

		color.Green("✅ Local model found: %s (%.2f MB)",
			cfg.ModelFile,
			float64(fileInfo.Size())/(1024*1024),
		)

		color.Blue("🔧 Loading LLaMA model...")
		if err := ai.LoadModel(cfg.ModelFile); err != nil {
			color.Red("❌ Failed to load model: %v", err)
			color.Yellow("Likely corrupted or incompatible file.")
			runEnhancedMockMode()
			return
		}

		defer ai.CloseModel()
		color.Green("✅ Local AI loaded successfully!")
	}

	// ---- Initialize RAG system ----
	color.Blue("🧠 Initializing RAG system...")
	ragSystem = rag.NewSystem(env)

	if ragSystem.IsInitialized() {
		color.Green("✅ RAG system READY")
	} else {
		stats := ragSystem.GetSystemStats()
		indexedPages := 0
		if pages, ok := stats["indexed_pages"]; ok {
			if p, ok := pages.(int); ok {
				indexedPages = p
			}
		}

		if indexedPages > 0 {
			color.Yellow("🔄 Resuming RAG indexing (%d pages)", indexedPages)
		} else {
			color.Yellow("📚 First-time RAG setup (manual pages indexing)")
		}

		ragSystem.IndexAvailableManPages()
		if indexedPages > 0 {
			color.Cyan("   Continuing from %d pages", indexedPages)
		}
	}

	// ---- Prompt Builder with RAG ----
	pb = ai.NewEnhancedPromptBuilder(env, online, ragSystem)

	if pb.IsRAGAvailable() {
		color.Green("🧠 RAG ACTIVE – enhanced command reasoning enabled")
	} else {
		color.Yellow("🔄 RAG indexing ongoing… will auto-enable soon")
		go monitorRAGInitialization(pb, ragSystem)
	}

	// ---- UX ----
	uxUI := ux.NewUX()
	uxUI.ShowWelcomeBanner("0.3.0")

	// ---- Minimal model test ----
	color.Blue("🧪 Running AI test...")

	testPrompts := []string{
		"Command to list files:",
		"ls",
		"List files command:",
	}

	for i, prompt := range testPrompts {
		resp, err := ai.RunModel(prompt)
		if err != nil {
			color.Red("❌ Test %d error: %v", i+1, err)
			continue
		}

		resp = strings.TrimSpace(resp)
		color.Cyan("Test %d result: %q", i+1, resp)

		if resp != "" {
			color.Green("✅ AI is responding correctly.")
			break
		}
	}

	// ---- Agent Mode Initialization ----
	agentCore = agent.NewAgent(
		env,
		pb,
		ragSystem,
		sandbox,
		execConfig,
		cfg.UserPrefs.TypingEffect,
	)

	color.Green("🎉 Helix is ready! Type '/help' for commands or just ask anything.")

	runEnhancedCLI()
}

// ---------------------------------------------
// RAG Monitoring
// ---------------------------------------------

func monitorRAGInitialization(pb *ai.PromptBuilder, ragSystem *rag.RAGSystem) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	timeout := time.After(90 * time.Second)
	checks := 0

	for {
		select {
		case <-ticker.C:
			checks++
			status := ragSystem.GetInitializationStatus()
			color.Cyan("🔄 RAG Status: %s", status)

			if ragSystem.IsInitialized() {
				color.Green("🎉 RAG system is now ACTIVE")
				return
			}

		case <-timeout:
			if !ragSystem.IsIndexingComplete() {
				color.Yellow("⏰ RAG initialization timed out")
			} else {
				color.Green("✅ RAG finished indexing during timeout window")
			}
			return
		}
	}
}

// ---------------------------------------------
// MOCK MODE
// ---------------------------------------------

func runEnhancedMockMode() {
	color.Yellow("\n🔧 ENHANCED MOCK MODE ENABLED (no real AI)")
	execConfig.DryRun = true
	env = shell.DetectEnvironment()
	pb = ai.NewPromptBuilder(env, online)

	reader := bufio.NewReader(os.Stdin)

	for {
		color.Cyan("[helix-mock]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch {
		case input == "/exit":
			color.Green("Exiting Helix. Goodbye!")
			return
		case input == "/debug":
			showDebugInfo()
		case input == "/help":
			showHelp()
		case strings.HasPrefix(input, "/cmd"):
			handleCmdCommand(input, true)
		case strings.HasPrefix(input, "/ask"):
			handleAskCommand(input, true)
		case strings.HasPrefix(input, "/explain"):
			handleExplainCommand(input, true)
		case strings.HasPrefix(input, "/install"):
			handleInstallCommand(input, true)
		case strings.HasPrefix(input, "/update"):
			handleUpdateCommand(input, true)
		case strings.HasPrefix(input, "/remove"):
			handleRemoveCommand(input, true)
		case strings.HasPrefix(input, "/dry-run"):
			toggleDryRun()
		default:
			color.Yellow("❓ Unknown command. Type '/help'")
		}
	}
}

// ---------------------------------------------
// ENHANCED CLI (Agent + Slash Commands)
// ---------------------------------------------

func runEnhancedCLI() {
	reader := bufio.NewReader(os.Stdin)
	lastRAGCheck := time.Now()
	ragShown := false

	for {
		color.Cyan("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Auto-enable RAG check
		if !ragShown && pb.IsRAGAvailable() {
			color.Green("🎉 RAG system is now ACTIVE")
			ragShown = true
		}

		if !pb.IsRAGAvailable() && time.Since(lastRAGCheck) > 30*time.Second {
			checkRAGProgress()
			lastRAGCheck = time.Now()
		}

		// Exit
		if input == "/exit" {
			color.Green("Goodbye! 👋")
			return
		}

		// Save history
		if input != "" {
			utils.AppendHistory(cfg.HistoryPath, input)
		}

		// Slash Command Routing
		switch {
		case input == "/debug":
			showDebugInfo()
		case input == "/help":
			showHelp()
		case input == "/online":
			checkOnlineStatus()
		case input == "/test-ai":
			testAIModel()
		case input == "/rag-status":
			handleRAGStatus()
		case input == "/rag-reindex":
			handleRAGReindex()
		case input == "/rag-reset":
			handleRAGReset()
		case input == "/test-basic-ai":
			testBasicAI()
		case strings.HasPrefix(input, "/cmd"):
			handleCmdCommand(input, false)
		case strings.HasPrefix(input, "/ask"):
			handleAskCommand(input, false)
		case strings.HasPrefix(input, "/explain"):
			handleExplainCommand(input, false)
		case strings.HasPrefix(input, "/install"):
			handleInstallCommand(input, false)
		case strings.HasPrefix(input, "/update"):
			handleUpdateCommand(input, false)
		case strings.HasPrefix(input, "/git"):
			handleGitCommand(input)
		case strings.HasPrefix(input, "/sandbox"):
			handleSandboxCommand(input)
		case strings.HasPrefix(input, "/cd"):
			handleChangeDirectory(input)
		case strings.HasPrefix(input, "/remove"):
			handleRemoveCommand(input, false)
		case strings.HasPrefix(input, "/dry-run"):
			toggleDryRun()

		// ---- AGENT MODE ----
		default:
			if input != "" {
				if agentCore != nil {
					agentCore.HandleInput(input)
				} else {
					color.Red("⚠️ Agent not initialized")
				}
			}
		}
	}
}

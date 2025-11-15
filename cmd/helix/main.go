package main

import (
	"bufio"
	"os"
	"strings"
	"time"

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
	aiProvider        AIProvider // NEW: which provider is active
	openAIAPIKey      string     // NEW: in-memory API key
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

	// Set execution config
	execConfig = commands.DefaultExecuteConfig()

	// Initialize Git manager
	gitManager = commands.NewGitManager(env, execConfig, sandbox)

	// Initialize syntax highlighter
	syntaxHighlighter = utils.NewSyntaxHighlighter()
	commands.SetSyntaxHighlighter(syntaxHighlighter)

	// Decide which AI provider to use
	selectedProvider := askForAIProvider()

	// If user chose OpenAI but we're offline, warn and fall back to local
	if selectedProvider == ai.ProviderOpenAI && !online {
		color.Red("❌ You appear to be offline, but OpenAI mode requires internet.")
		color.Yellow("💡 Falling back to local model mode.")
		selectedProvider = ai.ProviderLocal
	}

	if selectedProvider == ai.ProviderOpenAI {
		// Configure OpenAI provider
		if err := setupOpenAIProvider(); err != nil {
			color.Red("❌ Failed to configure OpenAI: %v", err)
			color.Yellow("Running in enhanced mock mode instead.")
			runEnhancedMockMode()
			return
		}

		ai.SetProvider(ai.ProviderOpenAI)
		color.Green("✅ Using OpenAI cloud provider for AI")
	} else {
		// Local model path (original behavior)
		ai.SetProvider(ai.ProviderLocal)

		// Ensure model directory exists
		if err := cfg.EnsureModelDir(); err != nil {
			color.Red("Error creating model directory: %v", err)
			return
		}

		// Download model if not present FIRST - before any other initialization
		color.Blue("📥 Checking for AI model...")
		if err := ai.DownloadModel(cfg.ModelFile, config.ModelURL, config.ModelChecksum); err != nil {
			color.Yellow("⚠️  Model download error: %v", err)
			color.Yellow("Running in enhanced mock mode.")
			runEnhancedMockMode()
			return
		}

		// Verify model file exists after download attempt
		fileInfo, err := os.Stat(cfg.ModelFile)
		if err != nil {
			color.Red("⚠️  Model file not found after download attempt: %v", err)
			color.Yellow("Running in enhanced mock mode.")
			runEnhancedMockMode()
			return
		}

		color.Green("✅ Model file exists: %s (Size: %.2f MB)",
			cfg.ModelFile,
			float64(fileInfo.Size())/(1024*1024))

		// Load LLaMA model
		color.Blue("🔧 Loading AI model...")
		if err := ai.LoadModel(cfg.ModelFile); err != nil {
			color.Red("⚠️  Failed to load model: %v", err)
			color.Yellow("This could indicate:")
			color.Yellow("  - Corrupted model file")
			color.Yellow("  - Incompatible model format")
			color.Yellow("  - Insufficient RAM/VRAM")

			runEnhancedMockMode()
			return
		}

		defer ai.CloseModel()
		color.Green("✅ AI model loaded successfully!")
	}

	// NOW initialize RAG system AFTER model is confirmed available
	color.Blue("🧠 Initializing RAG system...")
	ragSystem = rag.NewSystem(env)

	// Check RAG status and provide clear feedback
	if ragSystem.IsInitialized() {
		color.Green("✅ RAG system: READY (command documentation available)")
	} else {
		// Check if there's any existing progress
		stats := ragSystem.GetSystemStats()
		indexedPages := 0
		if pages, ok := stats["indexed_pages"]; ok {
			if p, ok := pages.(int); ok {
				indexedPages = p
			}
		}

		if indexedPages > 0 {
			color.Yellow("🔄 RAG system: RESUMING (%d pages already indexed)", indexedPages)
			color.Yellow("💡 RAG features will auto-enable when indexing completes")
		} else {
			color.Yellow("📚 RAG system: FIRST-TIME SETUP (indexing MAN pages)")
			color.Yellow("💡 This may take 1-2 minutes. RAG features will auto-enable when ready.")
		}

		// Start background indexing
		ragSystem.IndexAvailableManPages()

		// Show immediate status
		if indexedPages > 0 {
			color.Cyan("   Resuming from: %d pages", indexedPages)
		}
	}

	// Initialize prompt builder with RAG system reference
	pb = ai.NewEnhancedPromptBuilder(env, online, ragSystem)

	// Show initial RAG status - check immediately
	if pb.IsRAGAvailable() {
		color.Green("✅ RAG system: ACTIVE - enhanced prompts enabled")
	} else {
		status := ragSystem.GetInitializationStatus()
		color.Yellow("🔄 RAG system: %s - will auto-enable when ready", status)

		// Start monitoring RAG initialization with better tracking
		go monitorRAGInitialization(pb, ragSystem)
	}

	// Create UX manager for nice output
	ux := ux.NewUX()
	ux.ShowWelcomeBanner("0.3.0")

	// Test AI model with fallback check
	color.Blue("🧪 Testing AI model with fallback check...")

	// Test with different prompt styles
	testPrompts := []string{
		`Command to list files:`,
		`ls`,
		`List files command:`,
	}

	for i, prompt := range testPrompts {
		response, err := ai.RunModel(prompt)
		if err != nil {
			color.Red("❌ Test %d failed: %v", i+1, err)
			continue
		}

		cleanResponse := strings.TrimSpace(response)
		color.Cyan("Test %d: '%s'", i+1, cleanResponse)

		if cleanResponse != "" {
			color.Green("✅ Model is responding")
			break
		}

		if response == "" {
			color.Yellow("⚠️  Model responses are empty but command generation works")
		}
	}

	// Show final RAG status
	if pb.IsRAGAvailable() {
		color.Green("🧠 RAG system: ACTIVE (command documentation available)")
	} else {
		color.Yellow("🧠 RAG system: Indexing MAN pages in background...")
	}

	color.Green("🎉 Helix is ready! Type '/help' for available commands.")

	// Start enhanced CLI loop
	runEnhancedCLI()
}

// monitorRAGInitialization periodically checks if RAG system becomes initialized
func monitorRAGInitialization(pb *ai.PromptBuilder, ragSystem *rag.RAGSystem) {
	ticker := time.NewTicker(3 * time.Second) // More frequent checking
	defer ticker.Stop()

	// Reduced timeout since indexing should be faster now
	timeout := time.After(90 * time.Second) // 1.5 minutes timeout

	checks := 0
	maxChecks := 30 // 30 checks * 3 seconds = 90 seconds total

	for {
		select {
		case <-ticker.C:
			checks++

			// Use the new status method for better feedback
			status := ragSystem.GetInitializationStatus()
			color.Cyan("🔄 RAG Status: %s (check %d/%d)", status, checks, maxChecks)

			if ragSystem.IsInitialized() {
				color.Green("🎉 RAG system is now ACTIVE! Enhanced commands available.")
				return
			}

			// Stop if we've checked enough times
			if checks >= maxChecks {
				color.Yellow("⏰ RAG monitoring completed - system still initializing")
				color.Yellow("💡 RAG features will enable automatically when ready")
				return
			}

		case <-timeout:
			// Use the new method to check if indexing is complete
			if !ragSystem.IsIndexingComplete() {
				color.Yellow("⏰ RAG initialization timeout - continuing without RAG features")
			} else {
				color.Green("✅ RAG initialization completed during timeout window")
			}
			return
		}
	}
}

// runEnhancedMockMode remains the same (no RAG in mock mode)
func runEnhancedMockMode() {
	color.Yellow("\n🔧 ENHANCED MOCK MODE ACTIVATED")
	color.Yellow("AI commands will be simulated with intelligent responses")

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
			color.Green("Exiting Helix. Goodbye! 👋")
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
		case input == "/online":
			checkOnlineStatus()
		default:
			color.Yellow("❓ Unknown command. Type '/help' for available commands.")
		}
	}
}

// CLI loop to include RAG commands
func runEnhancedCLI() {
	reader := bufio.NewReader(os.Stdin)
	lastRAGCheck := time.Now()
	ragEnabledShown := false

	for {
		color.Cyan("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Use dynamic checking for RAG availability
		if !ragEnabledShown && pb.IsRAGAvailable() {
			color.Green("🎉 RAG system is now ACTIVE! Enhanced commands available.")
			ragEnabledShown = true
		}

		// Check RAG progress periodically if not ready
		if !pb.IsRAGAvailable() && time.Since(lastRAGCheck) > 30*time.Second {
			checkRAGProgress()
			lastRAGCheck = time.Now()
		}

		// Handle exit first
		if input == "/exit" {
			color.Green("Exiting Helix. Goodbye! 👋")
			return
		}

		// Save to history
		if input != "" {
			utils.AppendHistory(cfg.HistoryPath, input)
		}

		// Command handling
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
		default:
			if input != "" {
				color.Yellow("💡 Tip: Start with '/ask' for questions or '/cmd' for command generation")
			}
		}
	}
}

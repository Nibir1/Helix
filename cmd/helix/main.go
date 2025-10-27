package main

import (
	"bufio"
	"os"
	"strings"
	"time"

	"helix/internal/ai"
	"helix/internal/commands"
	"helix/internal/config"
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
)

func main() {
	// Initialize color output
	color.Cyan("🚀 Helix v%s — AI-Powered CLI Assistant", config.HelixVersion)
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

	// Initialize prompt builder
	pb = ai.NewPromptBuilder(env, online)

	// Initialize syntax highlighter
	syntaxHighlighter = utils.NewSyntaxHighlighter()
	commands.SetSyntaxHighlighter(syntaxHighlighter)

	// Ensure model directory exists
	if err := cfg.EnsureModelDir(); err != nil {
		color.Red("Error creating model directory: %v", err)
		return
	}

	// Download model if not present
	if err := ai.DownloadModel(cfg.ModelFile, config.ModelURL, config.ModelChecksum); err != nil {
		color.Yellow("⚠️  Model download error: %v", err)
		color.Yellow("Running in enhanced mock mode.")
		runEnhancedMockMode()
		return
	}

	// Verify model file
	fileInfo, err := os.Stat(cfg.ModelFile)
	if err != nil {
		color.Red("⚠️  Model file not found: %v", err)
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

	// Create UX manager for nice output
	ux := ux.NewUX()
	ux.ShowWelcomeBanner("0.3.0")

	// Test the model with a simple prompt
	color.Blue("🧪 Testing AI with simple prompt...")
	testResponse, err := ai.RunModel("Say 'Hello from Helix!' in one sentence:")
	if err != nil {
		color.Red("⚠️  Model test failed: %v", err)
		runEnhancedMockMode()
		return
	}

	color.Cyan("🤖 AI Test: %s", testResponse)
	color.Green("🎉 Helix is ready! Type '/help' for available commands.")

	// Start enhanced CLI loop
	runEnhancedCLI()
}

// runEnhancedMockMode starts the CLI in enhanced mock mode
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

// runEnhancedCLI starts the main CLI loop for Helix
func runEnhancedCLI() {
	reader := bufio.NewReader(os.Stdin)

	// Load command history
	history, _ := utils.LoadHistory(cfg.HistoryPath)
	if len(history) > 0 {
		color.Blue("📚 Loaded %d commands from history", len(history))
	}

	for {
		color.Cyan("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Save to history
		if input != "" && input != "/exit" {
			utils.AppendHistory(cfg.HistoryPath, input)
		}

		switch {
		case input == "/exit":
			color.Green("Exiting Helix. Goodbye! 👋")
			return
		case input == "/debug":
			showDebugInfo()
		case input == "/help":
			showHelp()
		case input == "/online":
			checkOnlineStatus()
		case input == "/test-ai":
			testAIModel()
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
			color.Yellow("💡 Tip: Start with '/ask' for questions or '/cmd' for command generation")
		}
	}
}

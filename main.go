// main.go
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
)

// Package-level variables
var (
	cfg        *Config
	env        Env
	pb         *PromptBuilder
	online     bool
	execConfig ExecuteConfig
)

func main() {
	// Initialize color output
	color.Cyan("🚀 Helix v%s — AI-Powered CLI Assistant", HelixVersion)
	color.Yellow("Repository: https://github.com/Nibir1/Helix")

	// Load configuration
	var err error
	cfg, err = DefaultConfig()
	if err != nil {
		color.Red("Error loading config: %v", err)
		return
	}

	// Detect environment
	env = DetectEnvironment()
	color.Blue("🌍 Detected: %s (%s shell)", strings.Title(env.OSName), env.Shell)

	// Check internet connectivity
	online = IsOnline(5 * time.Second)
	if online {
		color.Green("✅ Online mode - real-time capabilities available")
	} else {
		color.Yellow("⚠️  Offline mode - using local AI only")
	}

	// Initialize prompt builder
	pb = NewPromptBuilder(env, online)

	// Set execution config
	execConfig = DefaultExecuteConfig()

	// Ensure model directory exists
	if err := cfg.EnsureModelDir(); err != nil {
		color.Red("Error creating model directory: %v", err)
		return
	}

	// Download model if not present
	if err := DownloadModel(cfg.ModelFile, ModelURL, ModelChecksum); err != nil {
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
	if err := LoadModel(cfg.ModelFile); err != nil {
		color.Red("⚠️  Failed to load model: %v", err)
		color.Yellow("This could indicate:")
		color.Yellow("  - Corrupted model file")
		color.Yellow("  - Incompatible model format")
		color.Yellow("  - Insufficient RAM/VRAM")

		runEnhancedMockMode()
		return
	}

	defer CloseModel()
	color.Green("✅ AI model loaded successfully!")

	// Test the model with a simple prompt
	color.Blue("🧪 Testing AI with simple prompt...")
	testResponse, err := RunModel("Say 'Hello from Helix!' in one sentence:")
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

func runEnhancedMockMode() {
	color.Yellow("\n🔧 ENHANCED MOCK MODE ACTIVATED")
	color.Yellow("AI commands will be simulated with intelligent responses")

	execConfig.DryRun = true
	env = DetectEnvironment()
	pb = NewPromptBuilder(env, online)

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

func runEnhancedCLI() {
	reader := bufio.NewReader(os.Stdin)

	// Load command history
	history, _ := LoadHistory(cfg.HistoryPath)
	if len(history) > 0 {
		color.Blue("📚 Loaded %d commands from history", len(history))
	}

	for {
		color.Cyan("[helix]> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// Save to history
		if input != "" && input != "/exit" {
			AppendHistory(cfg.HistoryPath, input)
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
		case strings.HasPrefix(input, "/remove"):
			handleRemoveCommand(input, false)
		case strings.HasPrefix(input, "/dry-run"):
			toggleDryRun()
		default:
			color.Yellow("💡 Tip: Start with '/ask' for questions or '/cmd' for command generation")
		}
	}
}

// Command handlers for the enhanced CLI
func handleCmdCommand(input string, mockMode bool) {
	commandText := strings.TrimSpace(strings.TrimPrefix(input, "/cmd"))
	if commandText == "" {
		color.Red("❌ Usage: /cmd <natural language command>")
		color.Yellow("💡 Example: /cmd 'list all files in current directory'")
		return
	}

	// Build the prompt for command generation
	prompt := pb.BuildCommandPrompt(commandText)

	color.Blue("🤖 Processing: %s", commandText)

	var aiResponse string
	var err error

	if mockMode {
		// Mock AI response
		aiResponse = generateMockCommand(commandText, env)
		color.Green("🤖 [Mock AI] → %s", aiResponse)
	} else {
		// Real AI processing
		start := time.Now()
		aiResponse, err = RunModel(prompt)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
		color.Green("✅ AI processed in %s", FormatDuration(time.Since(start)))
	}

	// Extract the actual command from AI response
	command := ExtractCommand(aiResponse)

	if command == "" {
		color.Red("❌ AI didn't generate a valid command")
		color.Yellow("Raw AI response: %s", aiResponse)
		return
	}

	// Clean and validate the command
	cleanedCommand, err := ValidateAndCleanCommand(command)
	if err != nil {
		color.Red("❌ Command validation failed: %v", err)
		color.Yellow("Raw command: %s", command)
		return
	}
	command = cleanedCommand

	color.Cyan("💡 Generated command: %s", command)

	// Show the cleaned command for transparency
	if command != strings.TrimSpace(aiResponse) {
		color.Yellow("🔧 Note: Command was cleaned for safety")
	}

	// Ask for explanation if the command looks complex
	if shouldExplainCommand(command) && AskForConfirmation("Would you like an explanation of this command?") {
		explainCommand(command, mockMode)
	}

	// Execute the command
	if AskForConfirmation("Execute this command?") {
		err := ExecuteCommand(command, execConfig, env)
		if err != nil {
			color.Red("❌ Command failed: %v", err)
		} else {
			color.Green("✅ Command executed successfully!")
		}
	} else {
		color.Yellow("💡 Command ready to use: %s", command)
	}
}

func handleAskCommand(input string, mockMode bool) {
	promptText := strings.TrimSpace(strings.TrimPrefix(input, "/ask"))
	if promptText == "" {
		color.Red("❌ Usage: /ask <question>")
		color.Yellow("💡 Example: /ask 'how do I check disk space?'")
		return
	}

	color.Blue("🤖 Thinking about: %s", promptText)

	var response string
	var err error

	if mockMode {
		// Mock AI response
		response = generateMockResponse(promptText)
	} else {
		// Use a prompt that enforces English and concise responses
		prompt := fmt.Sprintf(`Instruction: Answer the following question in English. Be concise and direct.

Question: %s

Answer:`, promptText)

		// Use more restrictive parameters
		config := ModelConfig{
			Temperature: 0.3, // Lower for more deterministic responses
			TopP:        0.7,
			TopK:        20,
			MaxTokens:   150, // Limit response length
		}

		start := time.Now()
		response, err = RunModelWithConfig(prompt, config)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
		color.Green("✅ AI processed in %s", FormatDuration(time.Since(start)))

		// Debug: Show raw response
		color.Yellow("🔍 Raw AI response: '%s'", response)
	}

	// Basic cleaning
	response = strings.TrimSpace(response)

	if response == "" {
		color.Red("❌ AI generated an empty response")
		return
	}

	// Create UX manager for nice output
	ux := NewUX()
	ux.PrintAIResponse(response, !mockMode)
}

func handleExplainCommand(input string, mockMode bool) {
	commandText := strings.TrimSpace(strings.TrimPrefix(input, "/explain"))
	if commandText == "" {
		color.Red("❌ Usage: /explain <command>")
		color.Yellow("💡 Example: /explain 'git push origin main'")
		return
	}

	color.Blue("📚 Explaining command: %s", commandText)

	var explanation string
	var err error

	if mockMode {
		explanation = generateMockExplanation(commandText)
	} else {
		prompt := pb.BuildExplainPrompt(commandText)
		explanation, err = RunModel(prompt)
		if err != nil {
			color.Red("❌ AI error: %v", err)
			return
		}
	}

	ux := NewUX()
	ux.PrintAIResponse(explanation, !mockMode)
}

func handleInstallCommand(input string, mockMode bool) {
	args := strings.Fields(input)
	if len(args) < 2 {
		color.Red("❌ Usage: /install <package-name>")
		color.Yellow("💡 Example: /install git")
		return
	}

	action := "install"
	packageName := args[1]

	HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
}

func handleUpdateCommand(input string, mockMode bool) {
	args := strings.Fields(input)
	if len(args) < 2 {
		color.Red("❌ Usage: /update <package-name>")
		color.Yellow("💡 Example: /update git")
		return
	}

	action := "update"
	packageName := args[1]

	HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
}

func handleRemoveCommand(input string, mockMode bool) {
	args := strings.Fields(input)
	if len(args) < 2 {
		color.Red("❌ Usage: /remove <package-name>")
		color.Yellow("💡 Example: /remove git")
		return
	}

	action := "remove"
	packageName := args[1]

	HandlePackageCommand([]string{action, packageName}, env, mockMode, execConfig)
}

func showDebugInfo() {
	color.Cyan("=== 🔧 HELIX DEBUG INFORMATION ===")
	color.Cyan("Version: %s", HelixVersion)
	color.Cyan("Model: %s", cfg.ModelFile)
	color.Cyan("OS: %s", env.OSName)
	color.Cyan("Shell: %s", env.Shell)
	color.Cyan("User: %s", env.User)
	color.Cyan("Home: %s", env.HomeDir)
	color.Cyan("Online: %v", online)
	color.Cyan("Dry Run: %v", execConfig.DryRun)
	color.Cyan("Safe Mode: %v", execConfig.SafeMode)

	// Check model status
	if ModelIsLoaded() {
		color.Green("Model Status: ✅ Loaded")

		// Better model test - more specific and in English
		color.Blue("🧪 Running model test...")
		testResponse, err := RunModel("Answer with one word only: Hello")
		if err != nil {
			color.Red("Model Test: ❌ Failed - %v", err)
		} else {
			cleanResponse := strings.TrimSpace(testResponse)
			color.Green("Model Test: ✅ Working - '%s'", cleanResponse)

			// Check if response is reasonable
			if len(cleanResponse) > 100 {
				color.Yellow("⚠️  Model is generating verbose responses")
			}
			if !isMostlyEnglish(cleanResponse) {
				color.Yellow("⚠️  Model is responding in non-English")
			}
		}
	} else {
		color.Red("Model Status: ❌ Not loaded")
	}

	// Check package manager
	pkgMgr := DetectPackageManager(env)
	if pkgMgr.Exists {
		color.Green("Package Manager: %s", pkgMgr.Name)
	} else {
		color.Yellow("Package Manager: None detected")
	}

	// Check history
	history, _ := LoadHistory(cfg.HistoryPath)
	color.Cyan("Command History: %d entries", len(history))

	color.Cyan("=================================")
}

func showHelp() {
	ux := NewUX()
	ux.ShowHelp()
}

func checkOnlineStatus() {
	color.Blue("🌐 Checking internet connectivity...")

	if IsOnline(3 * time.Second) {
		color.Green("✅ Online - Real-time capabilities available")
	} else {
		color.Yellow("⚠️  Offline - Using local AI only")
	}
}

func toggleDryRun() {
	execConfig.DryRun = !execConfig.DryRun
	if execConfig.DryRun {
		color.Yellow("🔒 Dry-run mode ENABLED - commands will be shown but not executed")
	} else {
		color.Green("🚀 Dry-run mode DISABLED - commands will be executed")
	}
}

// Helper functions for mock mode
func generateMockCommand(request string, env Env) string {
	request = strings.ToLower(request)

	switch {
	case strings.Contains(request, "list") && strings.Contains(request, "file"):
		if env.IsUnixLike() {
			return "ls -la"
		} else {
			return "dir"
		}
	case strings.Contains(request, "current directory"):
		if env.IsUnixLike() {
			return "pwd"
		} else {
			return "cd"
		}
	case strings.Contains(request, "disk space"):
		if env.IsUnixLike() {
			return "df -h"
		} else {
			return "wmic logicaldisk get size,freespace,caption"
		}
	case strings.Contains(request, "process"):
		if env.IsUnixLike() {
			return "ps aux"
		} else {
			return "tasklist"
		}
	default:
		return "echo 'Mock command for: " + request + "'"
	}
}

func generateMockResponse(question string) string {
	question = strings.ToLower(question)

	switch {
	case strings.Contains(question, "what can you do") || strings.Contains(question, "help"):
		return "I can help you with:\n• Converting natural language to commands (/cmd)\n• Answering questions (/ask)\n• Explaining commands (/explain)\n• Managing packages (/install, /update, /remove)\n• And much more! Try /help for all commands."
	case strings.Contains(question, "president") && strings.Contains(question, "usa"):
		return "As an AI assistant running offline, I don't have real-time information about current political positions. You might want to check a reliable news source for the most up-to-date information."
	case strings.Contains(question, "weather"):
		return "I'm currently running in offline mode, so I can't access real-time weather data. You could try using online services or check your local weather app."
	case strings.Contains(question, "time"):
		return fmt.Sprintf("The current system time is: %s", time.Now().Format("Monday, January 2, 2006 at 3:04 PM"))
	case strings.Contains(question, "hello") || strings.Contains(question, "hi"):
		return "Hello! I'm Helix, your AI CLI assistant. How can I help you today?"
	default:
		return fmt.Sprintf("I understand you're asking about: '%s'. This is a simulated response since we're in mock mode. In real mode, I would provide a helpful answer based on my training data.", question)
	}
}

func generateMockExplanation(command string) string {
	return fmt.Sprintf("The command '%s' appears to be a system command. In mock mode, I can't provide detailed explanations, but in real mode I would explain what this command does, its common options, and any potential risks.", command)
}

func shouldExplainCommand(command string) bool {
	// Commands that might need explanation
	complexCommands := []string{
		"rm -", "chmod", "chown", "dd", "find", "grep", "sed", "awk",
		"curl", "wget", "ssh", "scp", "rsync", "tar", "gzip",
	}

	return ContainsAny(strings.ToLower(command), complexCommands)
}

func explainCommand(command string, mockMode bool) {
	color.Blue("📖 Getting explanation...")

	var explanation string
	var err error

	if mockMode {
		explanation = generateMockExplanation(command)
	} else {
		explanation, err = ExplainCommand(command)
		if err != nil {
			color.Red("❌ Explanation failed: %v", err)
			return
		}
	}

	ux := NewUX()
	ux.PrintAIResponse(explanation, !mockMode)
}

func testAIModel() {
	color.Cyan("🧪 Testing AI model with different prompts...")

	tests := []struct {
		name   string
		prompt string
	}{
		{"Simple Q&A", "Q: What is the sun?\nA:"},
		{"Instruction", "Instruction: Answer in one sentence. What is the sun?\nAnswer:"},
		{"Strict", "Answer the question in one word: Hello\nResponse:"},
		{"Chat", "User: What is the sun?\nAssistant:"},
	}

	for _, test := range tests {
		color.Blue("Testing: %s", test.name)
		response, err := RunModel(test.prompt)
		if err != nil {
			color.Red("  ❌ Failed: %v", err)
		} else {
			clean := strings.TrimSpace(response)
			color.Green("  ✅ Response: '%s'", clean)
			if len(clean) > 50 {
				color.Yellow("  ⚠️  Too verbose")
			}
		}
		time.Sleep(1 * time.Second) // Don't overwhelm the model
	}
}
